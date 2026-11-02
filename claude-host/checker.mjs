// The narrowing checker's sandbox. A checker session gets no built-in tools,
// none of the person's settings, hooks or MCP servers, and exactly one tool:
// report, which runs `agenthail ledger check-report` with an argument list,
// never through a shell. Every other tool is denied before it can run.
import { createSdkMcpServer, tool } from '@anthropic-ai/claude-agent-sdk';
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { z } from 'zod';

export const REPORT_TOOL = 'mcp__ledger__report';
export const CHECKER_ENV = { CLAUDE_CODE_ENABLE_TODO_TOOLS: '0', ENABLE_CLAUDEAI_MCP_SERVERS: 'false' };
const REPORT_TIMEOUT_MS = 30_000;
const DENIED = 'The checker can only report with mcp__ledger__report.';

const SYSTEM_PROMPT = `You check whether an agent's clauses capture everything a person asked for.
The first message gives the person's verbatim words, each with its utterance id, and the clauses an agent wrote from them.
Find every piece of breadth the words ask for that no clause covers, including any place a clause reads the words more narrowly than they allow.
Do not judge whether the work is feasible or wise, and do not invent requests the words do not make.
Answer only by calling the report tool once: list each missing piece with the utterance id it comes from, or report none.
You have no other tools and no access to files, commands or the web.`;

const AUTH_ENV = /^(ANTHROPIC_|AWS_|CLAUDE_CODE_USE_|CLAUDE_CODE_SKIP_|CLOUD_ML_|GOOGLE_|VERTEX_)|^(HTTPS?_PROXY|NO_PROXY|NODE_EXTRA_CA_CERTS|CLAUDE_CODE_API_KEY_HELPER_TTL_MS)$/;
const AUTH_SETTINGS = ['apiKeyHelper', 'awsAuthRefresh', 'awsCredentialExport'];

function personSettings(configDir) {
  const dir = configDir || process.env.CLAUDE_CONFIG_DIR || path.join(os.homedir(), '.claude');
  try {
    const settings = JSON.parse(fs.readFileSync(path.join(dir, 'settings.json'), 'utf8'));
    return settings && typeof settings === 'object' ? settings : {};
  } catch {
    return {};
  }
}

// carried is what of the person's settings a checker keeps: the default
// model and how Claude reaches its API (proxy, keys, key helper), nothing
// that adds tools, hooks or permissions.
export function carried(configDir) {
  const settings = personSettings(configDir);
  const env = {};
  for (const [name, value] of Object.entries(settings.env ?? {})) {
    if (AUTH_ENV.test(name) && typeof value === 'string') env[name] = value;
  }
  const auth = {};
  for (const name of AUTH_SETTINGS) if (typeof settings[name] === 'string') auth[name] = settings[name];
  return { model: typeof settings.model === 'string' ? settings.model : '', env, auth };
}

export function reportArgs(run, session, report) {
  const argv = ['ledger', 'check-report', run, '--from', session, '--json'];
  if (report.none) return [...argv, '--none'];
  for (const item of report.missing ?? []) argv.push('--missing', item.breadth, '--in', item.from);
  return argv;
}

function deny(toolName) {
  return { behavior: 'deny', message: `${DENIED} ${toolName} is not available.` };
}

export function checkerOptions({ run, sessionId, configDir, agenthail }) {
  const report = tool(
    'report',
    'Report what the clauses miss: each missing piece of breadth with the utterance id it comes from, or none.',
    {
      none: z.boolean().optional().describe('true when every request in the words is covered'),
      missing: z
        .array(z.object({ breadth: z.string().describe('the missing request, in clause form'), from: z.string().describe('the utterance id it comes from') }))
        .optional(),
    },
    (input) =>
      new Promise((resolve) => {
        if (!agenthail) return resolve({ isError: true, content: [{ type: 'text', text: 'agenthail is not available to this host' }] });
        execFile(agenthail, reportArgs(run, sessionId, input), { timeout: REPORT_TIMEOUT_MS, maxBuffer: 1 << 20 }, (error, stdout, stderr) => {
          const text = (error ? stderr || error.message : stdout).trim();
          resolve({ isError: Boolean(error), content: [{ type: 'text', text: text || 'reported' }] });
        });
      }),
  );
  const { model, env, auth } = carried(configDir);
  return {
    env: { ...env, ...CHECKER_ENV },
    ...(Object.keys(auth).length ? { settings: auth } : {}),
    tools: [],
    allowedTools: [REPORT_TOOL],
    settingSources: [],
    strictMcpConfig: true,
    mcpServers: { ledger: createSdkMcpServer({ name: 'ledger', version: '1.0.0', tools: [report] }) },
    permissionMode: 'dontAsk',
    systemPrompt: SYSTEM_PROMPT,
    ...(model ? { model } : {}),
    canUseTool: async (toolName, input) => (toolName === REPORT_TOOL ? { behavior: 'allow', updatedInput: input } : deny(toolName)),
    hooks: {
      PreToolUse: [
        {
          hooks: [
            async (input) =>
              input.tool_name === REPORT_TOOL
                ? { continue: true }
                : { hookSpecificOutput: { hookEventName: 'PreToolUse', permissionDecision: 'deny', permissionDecisionReason: deny(input.tool_name).message } },
          ],
        },
      ],
    },
  };
}
