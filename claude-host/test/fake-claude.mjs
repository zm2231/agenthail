#!/usr/bin/env node
// A stand-in for the Claude CLI in stream-json mode. It answers the SDK's
// control requests and runs each user message as a turn. FAKE_CLAUDE_DIR
// holds its transcript, one JSON line per turn, which also deduplicates
// message uuids across processes the way the real transcript does.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import readline from 'node:readline';
// The Agent SDK drops NODE_OPTIONS when it starts Claude, so the owner
// watchdog the host preloads has to be loaded here.
import './owner-watchdog.cjs';

const argv = process.argv.slice(2);
const flag = (name) => {
  const inline = argv.find((a) => a.startsWith(`--${name}=`));
  if (inline) return inline.slice(name.length + 3);
  const at = argv.indexOf(`--${name}`);
  return at >= 0 ? argv[at + 1] : '';
};
const sessionId = flag('session-id') || flag('resume');
let model = flag('model') || 'claude-opus-5-5';
let permissionMode = flag('permission-mode') || 'default';
// Claude reports each model's window in a turn result's modelUsage; a side
// query's model appears beside the main one.
const windows = { 'claude-opus-4-8': 200000 };
const modelUsage = () => ({ 'claude-haiku-5-5': { contextWindow: 64000 }, [model]: { contextWindow: windows[model] ?? 1000000 } });
const dir = process.env.FAKE_CLAUDE_DIR;
const configDir = process.env.CLAUDE_CONFIG_DIR || path.join(process.env.HOME, '.claude');
const transcript = path.join(dir, `${sessionId}.jsonl`);
// Like Claude, the fake reads its autocompact settings once, when it starts.
const settings = (() => {
  try {
    return JSON.parse(fs.readFileSync(path.join(configDir, 'settings.json'), 'utf8'));
  } catch {
    return {};
  }
})();
const contextReport = () => {
  const modelWindow = windows[model] ?? 1000000;
  if (settings.autoCompactEnabled === false) return { model, maxTokens: modelWindow, rawMaxTokens: modelWindow, isAutoCompactEnabled: false, autocompactSource: 'auto' };
  const window = Number.isInteger(settings.autoCompactWindow) ? Math.min(settings.autoCompactWindow, modelWindow) : modelWindow;
  return { model, maxTokens: window, rawMaxTokens: window, autoCompactThreshold: window - 33000, isAutoCompactEnabled: true, autocompactSource: Number.isInteger(settings.autoCompactWindow) ? 'settings' : 'auto' };
};
fs.appendFileSync(path.join(dir, 'spawns.jsonl'), JSON.stringify({ pid: process.pid, sessionId, argv, cwd: process.cwd(), env: Object.keys(process.env), configDir: process.env.CLAUDE_CONFIG_DIR ?? null, todoTools: process.env.CLAUDE_CODE_ENABLE_TODO_TOOLS ?? null, claudeaiMcp: process.env.ENABLE_CLAUDEAI_MCP_SERVERS ?? null }) + '\n');
// A refuse-<session id> file makes the CLI exit before it starts, printing the
// file's text the way Claude reports a session it cannot open.
const refusal = path.join(dir, `refuse-${sessionId}`);
if (fs.existsSync(refusal)) {
  process.stderr.write(fs.readFileSync(refusal, 'utf8'));
  process.exit(1);
}
// A started CLI writes diagnostics to stderr, which the host keeps as the
// session's log.
process.stderr.write(`fake-claude: session ${sessionId} started\n`);
const seen = new Set(
  fs.existsSync(transcript) ? fs.readFileSync(transcript, 'utf8').split('\n').filter(Boolean).map((l) => JSON.parse(l).uuid) : [],
);

// The commands Claude reports while it starts: two rows can share a name, and
// Claude's own row is the one /name runs. A SKILLS turn reports a new list, as
// Claude does when it finds a skill mid-session. FAKE_CLAUDE_TERMINAL_COMMANDS
// names commands the first turn's init message marks terminal-only.
const commands = [
  { name: 'review', description: 'A project command that shares a name', argumentHint: '' },
  { name: 'review', description: 'Review a pull request', argumentHint: '[pr]', builtin: true },
  { name: 'compact', description: 'Compact the conversation', argumentHint: '<instructions>', builtin: true },
  { name: 'statusline', description: 'Set up the status line', argumentHint: '', builtin: true },
  { name: '__internal', description: '', argumentHint: '', builtin: true },
  { name: 'l5-probe', description: 'A project skill', argumentHint: '' },
];
let initSent = false;

const write = (message) => {
  try {
    process.stdout.write(JSON.stringify(message) + '\n');
  } catch {}
};
process.stdout.on('error', () => {});
const pending = new Map();
let requests = 0;
let interrupt = null;
let turns = Promise.resolve();
let inputClosed = false;

// Claude reports its states to the SDK host only under
// CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS. The SDK sets
// CLAUDE_CODE_SDK_READS_SESSION_STATE, under which Claude marks them
// sdk_host_only and the SDK drops them. FAKE_CLAUDE_NO_STATE_EVENTS stands for
// a Claude that sends neither.
function state(value) {
  if (process.env.FAKE_CLAUDE_NO_STATE_EVENTS) return;
  const event = { type: 'system', subtype: 'session_state_changed', state: value, uuid: crypto.randomUUID(), session_id: sessionId };
  if (process.env.CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS) write(event);
  else if (process.env.CLAUDE_CODE_SDK_READS_SESSION_STATE) write({ ...event, sdk_host_only: true });
}

// tasks is the set of live background tasks, sent whole on every change.
const tasks = new Map();
function setTasks() {
  write({ type: 'system', subtype: 'background_tasks_changed', tasks: [...tasks.values()], uuid: crypto.randomUUID(), session_id: sessionId });
}

// Claude refuses a prompt still open when its input closes, and goes on.
const STREAM_CLOSED = 'Tool permission request failed: AbortError: Tool permission stream closed before response received';

// control asks the SDK and waits for its answer. An interrupt while it waits
// withdraws the request, the way Claude cancels a prompt nobody answered.
function control(request) {
  const id = `fake-${++requests}`;
  if (inputClosed) return Promise.resolve({ behavior: 'error', message: STREAM_CLOSED });
  write({ type: 'control_request', request_id: id, request });
  state('requires_action');
  return new Promise((resolve) => {
    pending.set(id, resolve);
    interrupt = () => {
      pending.delete(id);
      write({ type: 'control_cancel_request', request_id: id });
      resolve({ behavior: 'cancelled' });
    };
  });
}

// hooks holds the callback ids the SDK registered in initialize, by event.
// Like Claude, the fake calls them and records what each returned in
// hooks.jsonl.
let hooks = {};
async function runHooks(event, input) {
  for (const matcher of hooks[event] ?? []) {
    for (const callback of matcher.hookCallbackIds) {
      const id = `fake-${++requests}`;
      write({ type: 'control_request', request_id: id, request: { subtype: 'hook_callback', callback_id: callback, input: { session_id: sessionId, transcript_path: transcript, cwd: process.cwd(), hook_event_name: event, ...input } } });
      const output = await new Promise((resolve) => pending.set(id, resolve));
      fs.appendFileSync(path.join(dir, 'hooks.jsonl'), JSON.stringify({ sessionId, pid: process.pid, event, output }) + '\n');
    }
  }
}

async function turn(message) {
  const uuid = message.uuid;
  if (uuid && seen.has(uuid)) return;
  seen.add(uuid);
  const text = typeof message.message.content === 'string' ? message.message.content : JSON.stringify(message.message.content);
  await runHooks('UserPromptSubmit', { prompt: text });
  if (!initSent) {
    initSent = true;
    const terminal = process.env.FAKE_CLAUDE_TERMINAL_COMMANDS;
    write({ type: 'system', subtype: 'init', session_id: sessionId, uuid: crypto.randomUUID(), slash_commands: commands.map((c) => c.name), ...(terminal ? { terminal_slash_commands: terminal.split(',') } : {}) });
  }
  state('running');
  let reply = `echo:${text}`;
  if (text.startsWith('SKILLS')) {
    commands.push({ name: 'found-later', description: 'A skill found mid-session', argumentHint: '' });
    write({ type: 'system', subtype: 'commands_changed', commands, uuid: crypto.randomUUID(), session_id: sessionId });
  }
  if (text.startsWith('TOOL')) {
    const suggestions = [{ type: 'addRules', rules: [{ toolName: 'Bash', ruleContent: 'true' }], behavior: 'allow', destination: 'session' }];
    const decision = await control({ subtype: 'can_use_tool', tool_name: 'Bash', input: { command: 'true' }, tool_use_id: 'toolu_fake', title: 'Claude wants to run true', ...(text.includes('ALWAYS') ? { permission_suggestions: suggestions } : {}) });
    interrupt = null;
    state('running');
    reply = `tool:${decision.behavior}:${decision.message ?? ''}${decision.updatedPermissions ? ':always' : ''}`;
  } else if (text.startsWith('QUESTION')) {
    const questions = [{ question: 'Which color?', header: 'Color', multiSelect: false, options: [{ label: 'Red', description: 'Warm' }, { label: 'Blue', description: 'Cool' }] }];
    if (text.startsWith('QUESTIONS')) questions.push({ question: 'Which size?', header: 'Size', multiSelect: false, options: [{ label: 'S', description: 'Small' }, { label: 'L', description: 'Large' }] });
    const decision = await control({ subtype: 'can_use_tool', tool_name: 'AskUserQuestion', input: { questions }, tool_use_id: 'toolu_question' });
    interrupt = null;
    state('running');
    reply = `question:${decision.behavior}:${JSON.stringify(decision.updatedInput?.answers ?? null)}`;
  } else if (text.startsWith('ELICIT')) {
    // An MCP server asking for input: a form, or a link to open.
    const request = text.startsWith('ELICIT URL')
      ? { subtype: 'elicitation', mcp_server_name: 'docs', message: 'Sign in to docs', mode: 'url', url: 'https://example.com/auth', elicitation_id: 'e1' }
      : {
          subtype: 'elicitation',
          mcp_server_name: 'deploy',
          message: 'Where should this deploy?',
          requested_schema: {
            type: 'object',
            properties: {
              env: { type: 'string', title: 'Environment', enum: ['staging', 'prod'], enumNames: ['Staging', 'Production'] },
              replicas: { type: 'integer', title: 'Replicas', default: 2 },
              notify: { type: 'boolean', title: 'Notify the team' },
              regions: { type: 'array', title: 'Regions', items: { anyOf: [{ const: 'us', title: 'US' }, { const: 'eu', title: 'EU' }] } },
              note: { type: 'string', description: 'Why now' },
            },
            required: ['env'],
          },
        };
    const result = await control(request);
    interrupt = null;
    state('running');
    reply = `elicit:${result.action ?? result.behavior}:${JSON.stringify(result.content ?? null)}`;
  } else if (text.startsWith('MODE')) {
    // A mode Claude changes by itself, as leaving plan mode does.
    permissionMode = text.split(' ')[1];
    write({ type: 'system', subtype: 'status', status: null, permissionMode, uuid: crypto.randomUUID(), session_id: sessionId });
    reply = `mode:${permissionMode}`;
  } else if (text.startsWith('BURST')) {
    const count = Number(text.split(' ')[1]);
    for (let i = 0; i < count; i++) write({ type: 'system', subtype: 'status', status: null, burst: i });
    reply = `burst:${count}`;
  } else if (text.startsWith('SLOW')) {
    const ms = Number(text.split(' ')[1] ?? 30000);
    const interrupted = await new Promise((resolve) => {
      const timer = setTimeout(() => resolve(false), ms);
      interrupt = () => {
        clearTimeout(timer);
        resolve(true);
      };
    });
    interrupt = null;
    reply = interrupted ? 'interrupted' : `slow:${ms}`;
  } else if (text.startsWith('SHELL')) {
    // A background shell Claude started, in its process group.
    const shell = spawn('sleep', ['600'], { stdio: 'ignore' });
    reply = `shell:${shell.pid}`;
  } else if (text.startsWith('HANG')) {
    // A turn that ignores interrupts and the end of its input, like a wedged
    // Claude: only a kill ends it.
    await new Promise(() => {});
  } else if (text.startsWith('BACKGROUND')) {
    // A background shell that outlives the turn, like run_in_background.
    const ms = Number(text.split(' ')[1] ?? 30000);
    const id = `task-${uuid}`;
    tasks.set(id, { task_id: id, task_type: 'local_bash', description: 'background shell' });
    setTasks();
    setTimeout(() => finish(id), ms);
    reply = `background:${ms}`;
  } else if (text.startsWith('AMBIENT')) {
    // A task that is not activity, like a live-update watcher: it ends
    // without a turn telling the model.
    const ms = Number(text.split(' ')[1] ?? 30000);
    const id = `ambient-${uuid}`;
    tasks.set(id, { task_id: id, task_type: 'monitor', description: 'watcher', ambient: true });
    setTasks();
    setTimeout(() => finish(id, { turn: false }), ms);
    reply = `ambient:${ms}`;
  }
  fs.appendFileSync(transcript, JSON.stringify({ uuid, text, reply, origin: message.origin ?? null, priority: message.priority ?? null, pid: process.pid, inputClosed }) + '\n');
  claudeTranscript(uuid, text, reply);
  write({ type: 'assistant', uuid: crypto.randomUUID(), session_id: sessionId, parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'text', text: reply }] } });
  write({ type: 'result', subtype: 'success', is_error: false, result: reply, uuid: crypto.randomUUID(), session_id: sessionId, user_message_uuids: [uuid], duration_ms: 1, duration_api_ms: 1, num_turns: 1, total_cost_usd: 0, usage: {}, modelUsage: modelUsage() });
  if (process.env.FAKE_CLAUDE_IDLE_AFTER_MS) await new Promise((resolve) => setTimeout(resolve, Number(process.env.FAKE_CLAUDE_IDLE_AFTER_MS)));
  state('idle');
}

// finish ends a background task the way Claude 2.1.304 was seen to in SDK
// streaming mode: the notification, the task leaving the live set, and,
// FAKE_CLAUDE_NOTIFY_AFTER_MS later, a turn Claude starts by itself to tell
// the model. FAKE_CLAUDE_LEVEL_FIRST reverses the first two, which the SDK
// leaves unordered.
function finish(id, { turn = true } = {}) {
  const notification = { type: 'system', subtype: 'task_notification', task_id: id, status: 'completed', output_file: '', summary: 'background shell completed', uuid: crypto.randomUUID(), session_id: sessionId };
  tasks.delete(id);
  if (process.env.FAKE_CLAUDE_LEVEL_FIRST) setTasks();
  write(notification);
  if (!process.env.FAKE_CLAUDE_LEVEL_FIRST) setTasks();
  if (!turn) return;
  const gap = Number(process.env.FAKE_CLAUDE_NOTIFY_AFTER_MS ?? 100);
  turns = turns.then(() => new Promise((resolve) => setTimeout(resolve, gap))).then(() => notified(id));
}

function notified(id) {
  state('running');
  const reply = `notified:${id}`;
  fs.appendFileSync(transcript, JSON.stringify({ uuid: null, text: '<task-notification>', reply, origin: { kind: 'task-notification' }, priority: null, pid: process.pid, inputClosed }) + '\n');
  write({ type: 'assistant', uuid: crypto.randomUUID(), session_id: sessionId, parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'text', text: reply }] } });
  write({ type: 'result', subtype: 'success', is_error: false, result: reply, uuid: crypto.randomUUID(), session_id: sessionId, duration_ms: 1, duration_api_ms: 1, num_turns: 1, total_cost_usd: 0, usage: {}, modelUsage: modelUsage() });
  state('idle');
}

// claudeTranscriptFile is the conversation's transcript under its config
// root's projects, in the folder Claude names by its working directory.
function claudeTranscriptFile() {
  const projects = path.join(configDir, 'projects');
  const existing = fs.existsSync(projects)
    ? fs.readdirSync(projects).map((dir) => path.join(projects, dir, `${sessionId}.jsonl`)).find((file) => fs.existsSync(file))
    : undefined;
  const file = existing ?? path.join(projects, process.cwd().replaceAll('/', '-'), `${sessionId}.jsonl`);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  return file;
}

// With FAKE_CLAUDE_RECORD set each turn is also written to the conversation's
// transcript under its config root's projects in Claude's own record format, appending
// to an existing transcript of the conversation.
function claudeTranscript(uuid, text, reply) {
  if (!process.env.FAKE_CLAUDE_RECORD) return;
  const file = claudeTranscriptFile();
  const at = new Date().toISOString();
  const base = { sessionId, cwd: process.cwd(), timestamp: at };
  const records = [
    { ...base, type: 'user', uuid, message: { role: 'user', content: text } },
    { ...base, type: 'assistant', uuid: crypto.randomUUID(), message: { id: `msg-${uuid}`, model: model.replace(/\[.*$/, ''), role: 'assistant', stop_reason: 'end_turn', content: [{ type: 'text', text: reply }], usage: { input_tokens: 10, cache_creation_input_tokens: 0, cache_read_input_tokens: 990, output_tokens: 5 } } },
    { ...base, type: 'system', subtype: 'turn_duration' },
  ];
  fs.appendFileSync(file, records.map((r) => JSON.stringify(r)).join('\n') + '\n');
}

// With FAKE_CLAUDE_RECORD set it also writes the session record and serves
// the messaging socket a real Claude process has, so discovery finds it and
// native peer messages reach it.
if (process.env.FAKE_CLAUDE_RECORD) {
  // Like Claude, --name names the process and titles the conversation; a
  // process started without it gets a derived name, resumed or not.
  const naming = flag('name') ? { name: flag('name'), nameSource: 'user' } : { name: `fake-${process.pid}`, nameSource: 'derived' };
  if (flag('name')) fs.appendFileSync(claudeTranscriptFile(), JSON.stringify({ type: 'custom-title', customTitle: flag('name'), sessionId }) + '\n');
  const socket = `/tmp/cc-socks/${process.pid}.sock`;
  const record = path.join(configDir, 'sessions', `${process.pid}.json`);
  fs.mkdirSync('/tmp/cc-socks', { recursive: true, mode: 0o700 });
  fs.mkdirSync(path.dirname(record), { recursive: true });
  const cleanup = () => {
    for (const file of [socket, record]) {
      try {
        fs.unlinkSync(file);
      } catch {}
    }
  };
  process.on('exit', cleanup);
  // The socket is named by this process's pid, so one already there was left
  // by an earlier process with the same pid that was killed.
  fs.rmSync(socket, { force: true });
  net
    .createServer((conn) => {
      readline.createInterface({ input: conn }).on('line', (line) => {
        const frame = JSON.parse(line);
        if (frame.type !== 'user') return;
        const cause = holdCause(frame.message.content);
        if (cause) return hold(frame, cause);
        turns = turns.then(() => turn({ uuid: crypto.randomUUID(), origin: { kind: 'peer', from: frame.from }, message: frame.message }));
      });
    })
    .listen(socket, () => {
      fs.writeFileSync(record, JSON.stringify({ pid: process.pid, sessionId, cwd: process.cwd(), startedAt: Date.now(), kind: 'interactive', entrypoint: 'sdk-cli', status: 'idle', messagingSocketPath: socket, peerProtocol: 1, ...naming }));
    });
}

// Like Claude 2.1.305, a session that runs without permission prompts holds a
// peer message whose sender states no permission class or another one. A host
// has no prompt to approve it, so it is dropped once FAKE_CLAUDE_HOLD_MS (five
// minutes in Claude) pass, or once the file FAKE_CLAUDE_HOLD_RELEASE names
// exists.
function holdCause(content) {
  const own = permissionMode === 'bypassPermissions' ? 'bypass' : 'prompting';
  const stated = /^<cross-session-message [^>]*from-mode="(bypass|prompting)"/.exec(typeof content === 'string' ? content : '')?.[1];
  if (stated !== undefined) return stated === own ? '' : 'mode-mismatch';
  return own === 'bypass' ? 'no-mode-asserted' : '';
}

function hold(frame, cause) {
  const base = { type: 'system', subtype: 'peer_message_hold', lane: 'socket', from: frame.from ?? '', session_id: sessionId };
  const uuid = { message_uuid: typeof frame.uuid === 'string' ? frame.uuid : crypto.randomUUID() };
  write({ ...base, ...uuid, state: 'held', cause, uuid: crypto.randomUUID() });
  let dropped = false;
  const drop = () => {
    if (dropped) return;
    dropped = true;
    write({ ...base, ...uuid, state: 'dropped', outcome: 'expired', uuid: crypto.randomUUID() });
  };
  setTimeout(drop, Number(process.env.FAKE_CLAUDE_HOLD_MS ?? 300000));
  const release = process.env.FAKE_CLAUDE_HOLD_RELEASE;
  if (release) {
    const poll = setInterval(() => {
      if (fs.existsSync(release)) {
        clearInterval(poll);
        drop();
      }
    }, 20);
    poll.unref();
  }
}

const lines = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
lines.on('line', (line) => {
  const message = JSON.parse(line);
  if (message.type === 'control_request') {
    const subtype = message.request.subtype;
    if (subtype === 'interrupt') interrupt?.();
    if (subtype === 'set_model') model = message.request.model || 'claude-opus-5-5';
    if (subtype === 'initialize') {
      hooks = message.request.hooks ?? {};
      turns = turns.then(() => runHooks('SessionStart', { source: flag('resume') ? 'resume' : 'startup' }));
      fs.appendFileSync(path.join(dir, 'initialize.jsonl'), JSON.stringify({ sessionId, systemPrompt: message.request.systemPrompt ?? null, appendSystemPrompt: message.request.appendSystemPrompt ?? null }) + '\n');
    }
    if (subtype === 'set_permission_mode') {
      permissionMode = message.request.mode;
      write({ type: 'system', subtype: 'status', status: null, permissionMode, uuid: crypto.randomUUID(), session_id: sessionId });
    }
    if (subtype === 'set_model' || subtype === 'set_permission_mode') {
      fs.appendFileSync(path.join(dir, 'controls.jsonl'), JSON.stringify({ sessionId, pid: process.pid, ...message.request }) + '\n');
    }
    const responses = {
      initialize: { commands, models: [], account: {}, output_style: 'default', available_output_styles: [] },
      get_context_usage: { ...contextReport(), totalTokens: 1000, categories: [] },
    };
    const response = responses[subtype] ?? {};
    const answer = () => write({ type: 'control_response', response: { subtype: 'success', request_id: message.request_id, response } });
    // FAKE_CLAUDE_INITIALIZE_MS delays starting, as a slow disk or many plugins may.
    if (subtype === 'initialize' && process.env.FAKE_CLAUDE_INITIALIZE_MS) return void setTimeout(answer, Number(process.env.FAKE_CLAUDE_INITIALIZE_MS));
    // FAKE_CLAUDE_CONTEXT_REPORT_MS delays the context report, as a busy Claude may.
    if (subtype === 'get_context_usage' && process.env.FAKE_CLAUDE_CONTEXT_REPORT_MS) setTimeout(answer, Number(process.env.FAKE_CLAUDE_CONTEXT_REPORT_MS));
    else answer();
  } else if (message.type === 'control_response') {
    pending.get(message.response.request_id)?.(message.response.response);
  } else if (message.type === 'user') {
    turns = turns.then(() => turn(message));
  }
});
// Claude cannot ask anyone once its input closes: it refuses the prompts
// still open, finishes its turns and exits. With FAKE_CLAUDE_OUTLIVES_INPUT
// it keeps running and serving its messaging socket instead, as a Claude
// whose input an earlier host closed was seen doing. With FAKE_CLAUDE_LINGER_MS
// it closes its output and exits that much later. With
// FAKE_CLAUDE_LINGER_RELEASE it closes its output, writes <release>.<pid> to
// say it lingers, and exits once the release file exists.
lines.on('close', async () => {
  inputClosed = true;
  for (const [id, resolve] of [...pending]) {
    pending.delete(id);
    resolve({ behavior: 'error', message: STREAM_CLOSED });
  }
  await turns;
  if (process.env.FAKE_CLAUDE_OUTLIVES_INPUT) return setInterval(() => {}, 1 << 30);
  const release = process.env.FAKE_CLAUDE_LINGER_RELEASE;
  if (release) {
    process.stdout.end();
    const waiting = setInterval(() => {
      if (fs.existsSync(release)) process.exit(0);
    }, 20);
    fs.writeFileSync(`${release}.${process.pid}`, '');
    return waiting;
  }
  const linger = Number(process.env.FAKE_CLAUDE_LINGER_MS);
  if (linger) {
    process.stdout.end();
    return setTimeout(() => process.exit(0), linger);
  }
  process.exit(0);
});
process.on('SIGTERM', () => process.exit(0));
