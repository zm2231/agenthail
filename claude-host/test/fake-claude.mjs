#!/usr/bin/env node
// A stand-in for the Claude CLI in stream-json mode. It answers the SDK's
// control requests and runs each user message as a turn. FAKE_CLAUDE_DIR
// holds its transcript, one JSON line per turn, which also deduplicates
// message uuids across processes the way the real transcript does.
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import readline from 'node:readline';

const argv = process.argv.slice(2);
const flag = (name) => {
  const inline = argv.find((a) => a.startsWith(`--${name}=`));
  if (inline) return inline.slice(name.length + 3);
  const at = argv.indexOf(`--${name}`);
  return at >= 0 ? argv[at + 1] : '';
};
const sessionId = flag('session-id') || flag('resume');
const dir = process.env.FAKE_CLAUDE_DIR;
const configDir = process.env.CLAUDE_CONFIG_DIR || path.join(process.env.HOME, '.claude');
const transcript = path.join(dir, `${sessionId}.jsonl`);
fs.appendFileSync(path.join(dir, 'spawns.jsonl'), JSON.stringify({ pid: process.pid, sessionId, argv, cwd: process.cwd(), env: Object.keys(process.env), configDir: process.env.CLAUDE_CONFIG_DIR ?? null }) + '\n');
// A refuse-<session id> file makes the CLI exit before it starts, printing the
// file's text the way Claude reports a session it cannot open.
const refusal = path.join(dir, `refuse-${sessionId}`);
if (fs.existsSync(refusal)) {
  process.stderr.write(fs.readFileSync(refusal, 'utf8'));
  process.exit(1);
}
const seen = new Set(
  fs.existsSync(transcript) ? fs.readFileSync(transcript, 'utf8').split('\n').filter(Boolean).map((l) => JSON.parse(l).uuid) : [],
);

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

function control(request) {
  const id = `fake-${++requests}`;
  write({ type: 'control_request', request_id: id, request });
  return new Promise((resolve) => pending.set(id, resolve));
}

async function turn(message) {
  const uuid = message.uuid;
  if (uuid && seen.has(uuid)) return;
  seen.add(uuid);
  const text = typeof message.message.content === 'string' ? message.message.content : JSON.stringify(message.message.content);
  write({ type: 'system', subtype: 'session_state_changed', state: 'running', uuid: crypto.randomUUID(), session_id: sessionId });
  let reply = `echo:${text}`;
  if (text.startsWith('TOOL')) {
    const decision = await control({ subtype: 'can_use_tool', tool_name: 'Bash', input: { command: 'true' }, tool_use_id: 'toolu_fake' });
    reply = `tool:${decision.behavior}:${decision.message ?? ''}`;
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
  }
  fs.appendFileSync(transcript, JSON.stringify({ uuid, text, reply, origin: message.origin ?? null, priority: message.priority ?? null, pid: process.pid, inputClosed }) + '\n');
  claudeTranscript(uuid, text, reply);
  write({ type: 'assistant', uuid: crypto.randomUUID(), session_id: sessionId, parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'text', text: reply }] } });
  write({ type: 'result', subtype: 'success', is_error: false, result: reply, uuid: crypto.randomUUID(), session_id: sessionId, user_message_uuids: [uuid], duration_ms: 1, duration_api_ms: 1, num_turns: 1, total_cost_usd: 0, usage: {} });
  write({ type: 'system', subtype: 'session_state_changed', state: 'idle', uuid: crypto.randomUUID(), session_id: sessionId });
}

// With FAKE_CLAUDE_RECORD set each turn is also written to the conversation's
// transcript under its config root's projects in Claude's own record format, appending
// to an existing transcript of the conversation.
function claudeTranscript(uuid, text, reply) {
  if (!process.env.FAKE_CLAUDE_RECORD) return;
  const projects = path.join(configDir, 'projects');
  const existing = fs.existsSync(projects)
    ? fs.readdirSync(projects).map((dir) => path.join(projects, dir, `${sessionId}.jsonl`)).find((file) => fs.existsSync(file))
    : undefined;
  const file = existing ?? path.join(projects, 'fake-project', `${sessionId}.jsonl`);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  const at = new Date().toISOString();
  const base = { sessionId, cwd: process.cwd(), timestamp: at };
  const records = [
    { ...base, type: 'user', uuid, message: { role: 'user', content: text } },
    { ...base, type: 'assistant', uuid: crypto.randomUUID(), message: { id: `msg-${uuid}`, role: 'assistant', stop_reason: 'end_turn', content: [{ type: 'text', text: reply }] } },
    { ...base, type: 'system', subtype: 'turn_duration' },
  ];
  fs.appendFileSync(file, records.map((r) => JSON.stringify(r)).join('\n') + '\n');
}

// With FAKE_CLAUDE_RECORD set it also writes the session record and serves
// the messaging socket a real Claude process has, so discovery finds it and
// native peer messages reach it.
if (process.env.FAKE_CLAUDE_RECORD) {
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
        turns = turns.then(() => turn({ uuid: crypto.randomUUID(), origin: { kind: 'peer', from: frame.from }, message: frame.message }));
      });
    })
    .listen(socket, () => {
      fs.writeFileSync(record, JSON.stringify({ pid: process.pid, sessionId, cwd: process.cwd(), startedAt: Date.now(), kind: 'interactive', entrypoint: 'sdk-cli', status: 'idle', messagingSocketPath: socket, peerProtocol: 1 }));
    });
}

const lines = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
lines.on('line', (line) => {
  const message = JSON.parse(line);
  if (message.type === 'control_request') {
    const subtype = message.request.subtype;
    if (subtype === 'interrupt') interrupt?.();
    const response = subtype === 'initialize' ? { commands: [], models: [], account: {}, output_style: 'default', available_output_styles: [] } : {};
    write({ type: 'control_response', response: { subtype: 'success', request_id: message.request_id, response } });
  } else if (message.type === 'control_response') {
    pending.get(message.response.request_id)?.(message.response.response);
  } else if (message.type === 'user') {
    turns = turns.then(() => turn(message));
  }
});
lines.on('close', async () => {
  inputClosed = true;
  await turns;
  process.exit(0);
});
process.on('SIGTERM', () => process.exit(0));
