// Agenthail's Claude session host. One long-lived process runs every Claude
// session Agenthail opens, each as a streaming-input SDK query whose CLI is
// spawned detached in its own process group, so the host can exit or restart
// without ending a turn. The daemon talks to it over a unix socket with
// newline-delimited JSON requests ({id, method, params}) and responses
// ({id, result} or {id, error: {code, message}}); a subscribed connection also
// receives {event} lines.
import { query } from '@anthropic-ai/claude-agent-sdk';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import readline from 'node:readline';
import { parseArgs } from 'node:util';

const PROTOCOL = 1;
const LOCK_FD = 3;
const MAX_EVENTS = 4096;
const MAX_LINE = 1 << 20;
const MAX_SUBSCRIBER_BUFFER = 8 << 20;
const OPEN_TIMEOUT_MS = 60_000;
const TAKE_TIMEOUT_MS = 10_000;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const PERMISSION_MODES = new Set(['default', 'acceptEdits', 'bypassPermissions', 'plan', 'dontAsk', 'auto']);
const DENIED = 'Agenthail cannot answer permission prompts for this session; allow the tool through its permission mode or Claude settings.';

const { values: args } = parseArgs({
  options: {
    socket: { type: 'string' },
    state: { type: 'string' },
    claude: { type: 'string' },
    version: { type: 'string' },
  },
});
for (const name of ['socket', 'state', 'claude', 'version']) {
  if (!args[name]) fail(`--${name} is required`);
}
holdLock(path.join(args.state, 'lock'));

const sessions = new Map();
const orphans = loadOrphans(path.join(args.state, 'state.json'));
const events = [];
const subscribers = new Set();
let seq = 0;
let draining = false;

class HostError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

class Session {
  constructor(id, cwd) {
    this.id = id;
    this.cwd = cwd;
    this.pid = 0;
    this.started = '';
    this.state = 'starting';
    this.openedAt = new Date().toISOString();
    this.accepted = new Set();
    this.inbox = [];
    this.ended = false;
    this.wake = null;
    this.stderr = '';
    this.query = null;
    this.done = null;
  }

  async *input() {
    for (;;) {
      while (this.inbox.length) {
        const { message, taken } = this.inbox.shift();
        taken();
        yield message;
      }
      if (this.ended) return;
      await new Promise((resolve) => (this.wake = resolve));
      this.wake = null;
    }
  }

  push(message) {
    const taken = new Promise((resolve) => this.inbox.push({ message, taken: resolve }));
    this.wake?.();
    return taken;
  }

  end() {
    this.ended = true;
    this.wake?.();
  }

  setState(state) {
    if (this.state === state) return;
    this.state = state;
    emit(this.id, 'state', { state });
    if (draining && state === 'idle') this.end();
  }

  describe() {
    return { sessionId: this.id, pid: this.pid, cwd: this.cwd, state: this.state, openedAt: this.openedAt };
  }
}

const methods = {
  hello: () => ({ protocol: PROTOCOL, hostVersion: args.version, pid: process.pid }),
  open,
  send,
  interrupt: async ({ sessionId }) => {
    await session(sessionId).query.interrupt();
    return {};
  },
  close: async ({ sessionId }) => {
    const s = session(sessionId);
    s.end();
    await s.done;
    return {};
  },
  list: () => ({
    sessions: [...sessions.values()].map((s) => s.describe()),
    orphans: liveOrphans(),
    draining,
  }),
  drain: () => {
    draining = true;
    for (const s of sessions.values()) if (s.state === 'idle') s.end();
    exitWhenDrained();
    return { sessions: sessions.size };
  },
};

async function open(params) {
  if (draining) throw new HostError('draining', 'the Claude host is shutting down; open again on the next host');
  const cwd = params.cwd;
  if (typeof cwd !== 'string' || !path.isAbsolute(cwd) || !fs.statSync(cwd, { throwIfNoEntry: false })?.isDirectory()) {
    throw new HostError('invalid', `cwd must be an existing absolute directory: ${cwd}`);
  }
  if (params.resume && params.sessionId) throw new HostError('invalid', 'pass resume or sessionId, not both');
  const id = params.resume || params.sessionId || randomUUID();
  if (!UUID.test(id)) throw new HostError('invalid', `session id must be a uuid: ${id}`);
  if (params.permissionMode && !PERMISSION_MODES.has(params.permissionMode)) {
    throw new HostError('invalid', `unknown permission mode: ${params.permissionMode}`);
  }
  const existing = sessions.get(id);
  if (existing) return existing.describe();
  const orphan = liveOrphans().find((o) => o.sessionId === id);
  if (orphan) throw new HostError('held', `Claude process ${orphan.pid} from an earlier host still runs this session`);

  const s = new Session(id, cwd);
  sessions.set(id, s);
  let exited;
  const exit = new Promise((resolve) => (exited = resolve));
  s.query = query({
    prompt: s.input(),
    options: {
      cwd,
      env: process.env,
      pathToClaudeCodeExecutable: args.claude,
      ...(params.resume ? { resume: id } : { sessionId: id }),
      ...(params.permissionMode ? { permissionMode: params.permissionMode } : {}),
      ...(params.model ? { model: params.model } : {}),
      canUseTool: async (toolName, _input, { toolUseID }) => {
        emit(id, 'permission_denied', { tool: toolName, toolUseId: toolUseID, reason: DENIED });
        return { behavior: 'deny', message: DENIED };
      },
      spawnClaudeCodeProcess: (options) => spawnDetached(s, options, exited),
    },
  });
  s.done = run(s, exit);
  try {
    await Promise.race([
      s.query.initializationResult(),
      exit.then(() => Promise.reject(new Error(`Claude exited while starting: ${s.stderr.trim() || 'no output'}`))),
      timeout(OPEN_TIMEOUT_MS, 'Claude did not start'),
    ]);
  } catch (error) {
    s.end();
    s.query.close();
    throw new HostError('open_failed', error.message);
  }
  s.setState('idle');
  return s.describe();
}

async function send(params) {
  const s = session(params.sessionId);
  if (draining) throw new HostError('draining', 'the Claude host is shutting down; the message is delivered on the next host');
  if (typeof params.uuid !== 'string' || !UUID.test(params.uuid)) throw new HostError('invalid', 'uuid must be a uuid');
  if (typeof params.text !== 'string' || params.text.trim() === '') throw new HostError('invalid', 'text is required');
  if (s.accepted.has(params.uuid)) return { accepted: true, duplicate: true };
  const message = {
    type: 'user',
    uuid: params.uuid,
    session_id: s.id,
    parent_tool_use_id: null,
    message: { role: 'user', content: params.text },
  };
  if (params.priority) {
    if (!['now', 'next', 'later'].includes(params.priority)) throw new HostError('invalid', `unknown priority: ${params.priority}`);
    message.priority = params.priority;
  }
  if (params.origin) message.origin = origin(params.origin);
  s.accepted.add(params.uuid);
  const taken = s.push(message);
  s.setState('busy');
  try {
    await Promise.race([taken, timeout(TAKE_TIMEOUT_MS, 'Claude did not take the message')]);
  } catch (error) {
    throw new HostError('unknown', `${error.message}; it is delivered if Claude takes it later`);
  }
  return { accepted: true, duplicate: false };
}

function origin(value) {
  switch (value?.kind) {
    case 'human':
      return { kind: 'human' };
    case 'peer':
      if (typeof value.from !== 'string' || value.from === '') throw new HostError('invalid', 'peer origin needs from');
      return { kind: 'peer', from: value.from, ...(value.name ? { name: String(value.name) } : {}) };
    case 'channel':
      if (typeof value.server !== 'string' || value.server === '') throw new HostError('invalid', 'channel origin needs server');
      return { kind: 'channel', server: value.server };
    default:
      throw new HostError('invalid', `unknown origin kind: ${value?.kind}`);
  }
}

function spawnDetached(s, options, exited) {
  const child = spawn(options.command, options.args, {
    cwd: options.cwd,
    env: options.env,
    stdio: ['pipe', 'pipe', 'pipe'],
    detached: true,
  });
  s.pid = child.pid ?? 0;
  s.started = processStart(s.pid);
  child.stderr.on('data', (chunk) => {
    s.stderr = (s.stderr + chunk).slice(-4096);
  });
  child.on('exit', (code, signal) => exited({ code, signal }));
  child.on('error', (error) => exited({ code: null, signal: null, error: error.message }));
  options.signal?.addEventListener('abort', () => {
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM');
  });
  writeState();
  emit(s.id, 'opened', { pid: s.pid, cwd: s.cwd });
  return child;
}

async function run(s, exit) {
  let error = '';
  try {
    for await (const message of s.query) {
      if (message.type === 'system' && message.subtype === 'session_state_changed') {
        s.setState(message.state === 'idle' ? 'idle' : 'busy');
      } else if (message.type === 'result' && s.inbox.length === 0) {
        s.setState('idle');
      }
      emit(s.id, 'sdk', { message });
    }
  } catch (e) {
    error = e?.message ?? String(e);
  }
  s.ended = true;
  const status = await Promise.race([exit, timeout(10_000, 'exit').catch(() => ({ code: null, signal: null }))]);
  sessions.delete(s.id);
  writeState();
  emit(s.id, 'exited', { code: status.code, signal: status.signal, error: error || status.error || '', stderr: s.stderr.slice(-1024) });
  exitWhenDrained();
}

function session(id) {
  const s = sessions.get(id);
  if (!s || s.ended) throw new HostError('not_open', `Claude session ${id} is not open in the host`);
  return s;
}

function emit(sessionId, kind, data) {
  const event = { seq: ++seq, sessionId, kind, ...data };
  events.push(event);
  if (events.length > MAX_EVENTS) events.shift();
  const line = JSON.stringify({ event }) + '\n';
  for (const socket of subscribers) {
    if (socket.writableLength > MAX_SUBSCRIBER_BUFFER) {
      socket.destroy();
      subscribers.delete(socket);
    } else {
      socket.write(line);
    }
  }
}

function subscribe(socket, id, after) {
  const oldest = events.length ? events[0].seq : seq + 1;
  const gap = Number.isInteger(after) && after < oldest - 1;
  socket.write(JSON.stringify({ id, result: { seq, gap } }) + '\n');
  for (const event of events) if (event.seq > (after ?? seq)) socket.write(JSON.stringify({ event }) + '\n');
  subscribers.add(socket);
}

function serve(socket) {
  socket.on('error', () => {});
  socket.on('close', () => subscribers.delete(socket));
  const lines = readline.createInterface({ input: socket, crlfDelay: Infinity });
  lines.on('line', async (line) => {
    if (line.length > MAX_LINE) return socket.destroy();
    let request;
    try {
      request = JSON.parse(line);
    } catch {
      return socket.destroy();
    }
    const { id, method, params = {} } = request;
    if (method === 'subscribe') return subscribe(socket, id, params.after);
    const handler = Object.hasOwn(methods, method) ? methods[method] : null;
    try {
      if (!handler) throw new HostError('invalid', `unknown method: ${method}`);
      socket.write(JSON.stringify({ id, result: await handler(params) }) + '\n');
    } catch (error) {
      socket.write(JSON.stringify({ id, error: { code: error.code ?? 'internal', message: error.message } }) + '\n');
    }
  });
}

// The daemon takes the host lock and passes it as fd 3, so only one host runs
// per home and the lock lives exactly as long as this process. Node opens
// descriptors of its own, so the check is that fd 3 is that lock file.
function holdLock(lockPath) {
  let held;
  let expected;
  try {
    held = fs.fstatSync(LOCK_FD);
    expected = fs.statSync(lockPath);
  } catch {}
  if (!held || !expected || held.ino !== expected.ino || held.dev !== expected.dev) {
    fail('the Claude host runs only under the Agenthail daemon, which passes its lock');
  }
}

function loadOrphans(statePath) {
  let state;
  try {
    state = JSON.parse(fs.readFileSync(statePath, 'utf8'));
  } catch {
    return new Map();
  }
  const out = new Map();
  for (const [sessionId, entry] of Object.entries(state.sessions ?? {})) {
    if (runs(entry)) out.set(sessionId, { sessionId, pid: entry.pid, started: entry.started, cwd: entry.cwd, openedAt: entry.openedAt });
  }
  return out;
}

function liveOrphans() {
  for (const [id, orphan] of orphans) if (!runs(orphan)) orphans.delete(id);
  return [...orphans.values()];
}

function writeState() {
  const entries = {};
  for (const o of liveOrphans()) entries[o.sessionId] = { pid: o.pid, started: o.started, cwd: o.cwd, openedAt: o.openedAt };
  for (const s of sessions.values()) if (s.pid) entries[s.id] = { pid: s.pid, started: s.started, cwd: s.cwd, openedAt: s.openedAt };
  const target = path.join(args.state, 'state.json');
  const temp = `${target}.${process.pid}.tmp`;
  fs.writeFileSync(temp, JSON.stringify({ pid: process.pid, sessions: entries }) + '\n', { mode: 0o600 });
  fs.renameSync(temp, target);
}

// A recorded Claude process still runs only if its pid has the start time it
// had when the host spawned it; a reused pid belongs to another process.
function runs(entry) {
  return alive(entry.pid) && entry.started !== '' && entry.started === processStart(entry.pid);
}

function processStart(pid) {
  const ps = spawnSync('ps', ['-o', 'lstart=', '-p', String(pid)], { encoding: 'utf8', env: { ...process.env, LC_ALL: 'C' } });
  return ps.status === 0 ? ps.stdout.trim() : '';
}

function alive(pid) {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return error.code === 'EPERM';
  }
}

function timeout(ms, what) {
  return new Promise((_, reject) => setTimeout(() => reject(new Error(`${what} within ${ms / 1000}s`)), ms).unref());
}

function exitWhenDrained() {
  if (draining && sessions.size === 0) shutdown(0);
}

function shutdown(code) {
  server.close();
  try {
    fs.unlinkSync(args.socket);
  } catch {}
  process.exit(code);
}

function fail(message) {
  process.stderr.write(`claude-host: ${message}\n`);
  process.exit(2);
}

try {
  fs.unlinkSync(args.socket);
} catch {}
const server = net.createServer(serve);
server.listen(args.socket, () => {
  fs.chmodSync(args.socket, 0o600);
  writeState();
  process.stderr.write(`claude-host: pid ${process.pid} protocol ${PROTOCOL} version ${args.version} listening on ${args.socket}\n`);
});
process.on('SIGTERM', () => shutdown(0));
process.on('SIGINT', () => shutdown(0));
