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

const PROTOCOL = 3;
const LOCK_FD = 3;
const MAX_EVENTS = 4096;
const MAX_LINE = 1 << 20;
const MAX_SUBSCRIBER_BUFFER = 8 << 20;
const MAX_IMAGE_BYTES = 5 << 20;
const IMAGE_TYPES = new Set(['image/png', 'image/jpeg', 'image/gif']);
const OPEN_TIMEOUT_MS = 60_000;
const TAKE_TIMEOUT_MS = 10_000;
// How long close waits for Claude to exit after interrupting its turn and
// closing its input, before it kills the process.
const CLOSE_GRACE_MS = Number(process.env.AGENTHAIL_CLAUDE_HOST_CLOSE_GRACE_MS) || 10_000;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const PERMISSION_MODES = new Set(['default', 'manual', 'acceptEdits', 'bypassPermissions', 'plan', 'dontAsk', 'auto']);
const EFFORTS = new Set(['low', 'medium', 'high', 'xhigh', 'max']);
const MAX_REQUEST_INPUT = 16 << 10;
const MAX_REQUEST_TEXT = 2000;
const MAX_SETTINGS = 2000;
const DECLINED = 'The person declined this request.';
const WITHDRAWN = 'The request was withdrawn before anyone answered it.';

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
const settings = loadSettings(path.join(args.state, 'settings.json'));
const events = [];
const subscribers = new Set();
let seq = 0;
let draining = false;
let exiting = false;
let listening = args.socket;
let retiring = null;
let stateEventsMissing = false;

class HostError extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
}

// StartError is a Claude process that ended before it initialized. One that
// ran and exited with an error code refused the conversation, such as a
// workspace it does not trust; one that could not be spawned or was killed
// did not get to decide.
class StartError extends Error {
  constructor(message, status) {
    super(message);
    this.refused = status.code !== null && status.code !== 0 && !status.error;
  }
}

class Session {
  constructor(id, cwd, permissionMode, model) {
    this.id = id;
    this.cwd = cwd;
    this.permissionMode = permissionMode;
    this.model = model;
    this.pending = new Map();
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
    this.stateEvents = false;
    this.guessesIdle = false;
    this.tasks = [];
    this.taskSince = new Map();
    this.owed = new Set();
    this.ambient = new Set();
    this.context = null;
    this.measuring = null;
  }

  // measureAfter records, off the message loop and one result at a time, the
  // context window Claude runs the main model with. A turn result counts
  // usage for every model the turn called, side queries included, so Claude's
  // own context report names which entry is the main model's. The transcript
  // records no window size.
  measureAfter(result) {
    const measuring = (this.measuring ?? Promise.resolve()).then(() => this.measure(result)).catch(() => {});
    this.measuring = measuring;
    measuring.then(() => {
      if (this.measuring === measuring) this.measuring = null;
    });
  }

  async measure(result) {
    const usage = await Promise.race([this.query.getContextUsage({ detail: 'summary' }), timeout(5_000, 'context report')]);
    const window = result.modelUsage?.[usage?.model]?.contextWindow;
    if (!Number.isInteger(window) || window <= 0) return;
    if (this.context?.window === window && this.context?.model === usage.model) return;
    this.context = { window, model: usage.model };
    emit(this.id, 'context', { contextWindow: window, contextModel: usage.model });
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

  // end closes Claude's input. Claude then refuses every prompt it still
  // waits on, so those requests end here rather than stay listed unanswerable.
  end() {
    this.ended = true;
    this.endRequests();
    this.wake?.();
  }

  endRequests() {
    for (const pending of [...this.pending.values()]) pending.settle('ended', { behavior: 'deny', message: WITHDRAWN });
  }

  // owe records work Claude was handed while idle and has yet to start a turn
  // for: a message the host passed it, or a background task that ended, which
  // Claude reports idle before it starts the turn telling the model. Claude
  // reports idle only with its queue empty, so a turn it starts takes all of
  // it; a message it answers is also known by the uuids its replies carry.
  owe(key) {
    this.owed.add(key);
  }

  turnStarted() {
    this.owed.clear();
  }

  setState(state) {
    if (this.state === state) return;
    this.state = state;
    emit(this.id, 'state', { state });
    this.retireIfDone();
  }

  // A retiring host closes a session only once Claude is idle with no open
  // request, no background task and no turn owed, since closing ends the
  // process and every task it runs. The next message resumes the conversation
  // on the new host.
  // A Claude that ended a turn without reporting its states leaves idle a
  // guess that a turn it started itself may still be running, so such a
  // session retires only as a turn result arrives.
  retireIfDone({ turnEnded = false } = {}) {
    if (this.guessesIdle && !turnEnded) return;
    if (!draining || this.ended || !this.query || this.state !== 'idle' || this.pending.size > 0 || this.tasks.length > 0 || this.owed.size > 0) return;
    if (this.measuring) {
      this.measuring.then(() => this.retireIfDone({ turnEnded }));
      return;
    }
    process.stderr.write(`claude-host: retiring session ${this.id} (pid ${this.pid}), now idle\n`);
    this.end();
    this.query.close();
  }

  // ask holds a permission request or question until a person answers it.
  // It has no deadline: it ends only with an answer, Claude withdrawing it
  // (an interrupt or a turn that ended), or the session's end.
  ask(tool, input, options) {
    const request = permissionRequest(tool, input, options);
    if (this.ended) return Promise.resolve({ behavior: 'deny', message: WITHDRAWN });
    return new Promise((resolve) => {
      const withdraw = () => settle('withdrawn', { behavior: 'deny', message: WITHDRAWN });
      const settle = (outcome, result) => {
        if (!this.pending.delete(request.requestId)) return;
        options.signal?.removeEventListener('abort', withdraw);
        emit(this.id, 'request_resolved', { requestId: request.requestId, outcome });
        resolve(result);
        this.retireIfDone();
      };
      this.pending.set(request.requestId, { request, input, suggestions: options.suggestions, settle });
      emit(this.id, 'request_opened', { request });
      if (options.signal?.aborted) withdraw();
      else options.signal?.addEventListener('abort', withdraw, { once: true });
    });
  }

  remember() {
    settings.set(this.id, { permissionMode: this.permissionMode, model: this.model, updatedAt: new Date().toISOString() });
    writeSettings();
  }

  describe() {
    return {
      sessionId: this.id,
      pid: this.pid,
      cwd: this.cwd,
      state: this.state,
      openedAt: this.openedAt,
      permissionMode: this.permissionMode,
      model: this.model,
      contextWindow: this.context?.window ?? 0,
      contextModel: this.context?.model ?? '',
      requests: [...this.pending.values()].map((p) => p.request),
      tasks: this.describeTasks(),
    };
  }

  // describeTasks lists the live background tasks Claude last reported, with
  // when this host first saw each one.
  describeTasks() {
    return this.tasks.map((task) => ({
      id: task.task_id,
      type: task.task_type ?? '',
      description: task.description ?? '',
      parentId: task.parent_task_id ?? '',
      ambient: task.ambient === true,
      since: this.taskSince.get(task.task_id),
    }));
  }
}

const methods = {
  // closeStops tells clients that close interrupts a running turn, so they need
  // not interrupt first.
  hello: () => ({ protocol: PROTOCOL, hostVersion: args.version, pid: process.pid, closeStops: true }),
  open,
  send,
  interrupt: async ({ sessionId }) => {
    await session(sessionId).query.interrupt();
    return {};
  },
  respond: ({ sessionId, requestId, decision }) => {
    const s = session(sessionId);
    const pending = s.pending.get(requestId);
    if (!pending) throw new HostError('stale', `request ${requestId} is not open in session ${sessionId}: it was answered or withdrawn`);
    const { outcome, result } = decide(pending, decision);
    pending.settle(outcome, result);
    return { outcome };
  },
  setModel: async ({ sessionId, model }) => {
    if (typeof model !== 'string' || model === '' || /\s/.test(model)) throw new HostError('invalid', 'model must be one model name');
    const s = session(sessionId);
    await s.query.setModel(model);
    s.model = model;
    s.remember();
    return s.describe();
  },
  setPermissionMode: async ({ sessionId, permissionMode }) => {
    if (!PERMISSION_MODES.has(permissionMode)) throw new HostError('invalid', `unknown permission mode: ${permissionMode}`);
    const s = session(sessionId);
    await s.query.setPermissionMode(permissionMode);
    s.permissionMode = permissionMode;
    s.remember();
    emit(s.id, 'permission_mode', { permissionMode });
    return s.describe();
  },
  // close stops the session now: it interrupts a running turn, closes
  // Claude's input and returns once the process has exited, killing it if it
  // has not within the grace period.
  close: async ({ sessionId }) => {
    const s = session(sessionId);
    if (s.state !== 'idle') {
      s.endRequests();
      await s.query?.interrupt().catch(() => {});
    }
    s.end();
    let timer;
    const exited = await Promise.race([s.done.then(() => true), new Promise((resolve) => (timer = setTimeout(() => resolve(false), CLOSE_GRACE_MS)))]);
    clearTimeout(timer);
    if (!exited) {
      process.stderr.write(`claude-host: Claude ${s.pid} for session ${s.id} did not exit ${CLOSE_GRACE_MS} ms after close; killing it\n`);
      killSession(s);
      await s.done;
    }
    // done also settles when Claude ends its output but its process lingers.
    killSession(s);
    return {};
  },
  list: () => ({
    sessions: [...sessions.values()].map((s) => s.describe()),
    orphans: liveOrphans(),
    draining,
  }),
  drain: async () => {
    retiring ??= retire().catch((error) => {
      retiring = null;
      throw error;
    });
    await retiring;
    for (const s of [...sessions.values()]) s.retireIfDone();
    setTimeout(exitWhenDrained, 100);
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
  if (params.effort && !EFFORTS.has(params.effort)) throw new HostError('invalid', `unknown effort: ${params.effort}`);
  for (const name of ['model', 'agent', 'name', 'worktree']) {
    if (params[name] !== undefined && typeof params[name] !== 'string') throw new HostError('invalid', `${name} must be a string`);
  }
  if (params.resume && params.worktree) throw new HostError('invalid', 'a resumed session keeps its worktree');
  if (params.configDir !== undefined && (typeof params.configDir !== 'string' || !path.isAbsolute(params.configDir))) {
    throw new HostError('invalid', `configDir must be an absolute path: ${params.configDir}`);
  }
  const extraArgs = {
    ...(params.name ? { name: params.name } : {}),
    ...(params.worktree ? { worktree: params.worktree } : {}),
  };
  const existing = sessions.get(id);
  if (existing) return existing.describe();
  refuseOrphaned(id);

  // Claude starts a resumed conversation in the settings' default mode and
  // model, so a resume without them reopens in the ones it last ran with here.
  const kept = params.resume ? settings.get(id) : undefined;
  const permissionMode = params.permissionMode || kept?.permissionMode || '';
  const model = params.model || kept?.model || '';
  const s = new Session(id, cwd, permissionMode, model);
  sessions.set(id, s);
  let exited;
  const exit = new Promise((resolve) => (exited = resolve));
  try {
    s.query = query({
      prompt: s.input(),
      options: {
        cwd,
        // Claude reports its own idle, busy and requires_action states only
        // when asked; the SDK otherwise reads them and drops them.
        env: { ...process.env, CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS: '1', ...(params.configDir ? { CLAUDE_CONFIG_DIR: params.configDir } : {}) },
        pathToClaudeCodeExecutable: args.claude,
        ...(params.resume ? { resume: id } : { sessionId: id }),
        ...(permissionMode ? { permissionMode } : {}),
        ...(model ? { model } : {}),
        ...(params.effort ? { effort: params.effort } : {}),
        ...(params.agent ? { agent: params.agent } : {}),
        extraArgs,
        // Every session may be switched into bypassPermissions later, which
        // Claude allows only when it started with consent to it. Claude still
        // asks canUseTool for questions in bypassPermissions.
        allowDangerouslySkipPermissions: true,
        canUseTool: (toolName, input, options) => s.ask(toolName, input, options),
        spawnClaudeCodeProcess: (options) => spawnDetached(s, options, exited),
      },
    });
    s.done = run(s, exit);
    await Promise.race([
      s.query.initializationResult(),
      exit.then((status) => Promise.reject(new StartError(`Claude exited while starting: ${s.stderr.trim() || 'no output'}`, status))),
      timeout(OPEN_TIMEOUT_MS, 'Claude did not start'),
    ]);
  } catch (error) {
    s.end();
    s.query?.close();
    if (!s.done) sessions.delete(id);
    throw new HostError(error instanceof StartError && error.refused ? 'refused' : 'open_failed', error.message);
  }
  if (params.permissionMode || params.model) s.remember();
  s.setState('idle');
  return s.describe();
}

function permissionRequest(tool, input, options) {
  const request = {
    requestId: typeof options.requestId === 'string' && options.requestId ? options.requestId : randomUUID(),
    toolUseId: options.toolUseID ?? '',
    tool,
    kind: tool === 'AskUserQuestion' ? 'question' : 'approval',
    createdAt: new Date().toISOString(),
    alwaysAllow: Array.isArray(options.suggestions) && options.suggestions.length > 0 && !options.suppressAlwaysAllowRule,
  };
  for (const name of ['title', 'displayName', 'description', 'decisionReason', 'blockedPath']) {
    if (typeof options[name] === 'string' && options[name] !== '') request[name] = options[name].slice(0, MAX_REQUEST_TEXT);
  }
  if (typeof options.agentID === 'string' && options.agentID !== '') request.agentId = options.agentID;
  if (request.kind === 'question') {
    request.questions = (Array.isArray(input?.questions) ? input.questions : []).map((q) => ({
      question: String(q?.question ?? ''),
      header: String(q?.header ?? ''),
      multiSelect: q?.multiSelect === true,
      options: (Array.isArray(q?.options) ? q.options : []).map((o) => ({ label: String(o?.label ?? ''), description: String(o?.description ?? '') })),
    }));
  } else {
    const text = JSON.stringify(input ?? {});
    request.input = text.length <= MAX_REQUEST_INPUT ? (input ?? {}) : { truncated: text.slice(0, MAX_REQUEST_INPUT) };
  }
  return request;
}

// decide turns a person's decision into Claude's permission result. A question
// is answered only with an answer to each of its questions, keyed by the
// question's text; a free-text answer stands for Claude's own "Other".
function decide({ request, input, suggestions }, decision) {
  if (decision?.behavior === 'deny') {
    const message = typeof decision.message === 'string' && decision.message.trim() !== '' ? decision.message.trim() : DECLINED;
    return { outcome: 'denied', result: { behavior: 'deny', message } };
  }
  if (decision?.behavior !== 'allow') throw new HostError('invalid', 'decision.behavior must be allow or deny');
  if (request.kind === 'question') {
    const answers = decision.answers;
    if (!answers || typeof answers !== 'object' || Array.isArray(answers)) throw new HostError('invalid', 'a question is answered with answers');
    for (const key of Object.keys(answers)) {
      if (!request.questions.some((q) => q.question === key)) throw new HostError('invalid', `not a question of this request: ${key}`);
    }
    for (const q of request.questions) {
      if (typeof answers[q.question] !== 'string' || answers[q.question].trim() === '') throw new HostError('invalid', `no answer to: ${q.question}`);
    }
    return { outcome: 'answered', result: { behavior: 'allow', updatedInput: { ...input, answers } } };
  }
  if (decision.answers !== undefined) throw new HostError('invalid', 'only a question takes answers');
  if (decision.always) {
    if (!request.alwaysAllow) throw new HostError('invalid', 'this request offers no rule to always allow');
    return { outcome: 'allowed_always', result: { behavior: 'allow', updatedInput: input, updatedPermissions: suggestions } };
  }
  return { outcome: 'allowed', result: { behavior: 'allow', updatedInput: input } };
}

async function send(params) {
  const s = session(params.sessionId);
  if (typeof params.uuid !== 'string' || !UUID.test(params.uuid)) throw new HostError('invalid', 'uuid must be a uuid');
  const images = params.images ?? [];
  if (!Array.isArray(images)) throw new HostError('invalid', 'images must be a list');
  if (typeof params.text !== 'string' || (params.text.trim() === '' && images.length === 0)) throw new HostError('invalid', 'text is required');
  if (s.accepted.has(params.uuid)) return { accepted: true, duplicate: true };
  const message = {
    type: 'user',
    uuid: params.uuid,
    session_id: s.id,
    parent_tool_use_id: null,
    message: { role: 'user', content: images.length === 0 ? params.text : userContent(params.text, images) },
  };
  if (params.priority) {
    if (!['now', 'next', 'later'].includes(params.priority)) throw new HostError('invalid', `unknown priority: ${params.priority}`);
    message.priority = params.priority;
  }
  if (params.origin) message.origin = origin(params.origin);
  s.accepted.add(params.uuid);
  s.owe(params.uuid);
  const taken = s.push(message);
  s.setState('busy');
  try {
    await Promise.race([taken, timeout(TAKE_TIMEOUT_MS, 'Claude did not take the message')]);
  } catch (error) {
    throw new HostError('unknown', `${error.message}; it is delivered if Claude takes it later`);
  }
  return { accepted: true, duplicate: false };
}

// Images come first, as Claude Code records a pasted image before the text.
function userContent(text, images) {
  const blocks = images.map((image) => {
    if (!IMAGE_TYPES.has(image?.mediaType)) throw new HostError('invalid', `unsupported image type: ${image?.mediaType}`);
    if (typeof image.path !== 'string' || !path.isAbsolute(image.path)) throw new HostError('invalid', 'image path must be absolute');
    let data;
    try {
      data = fs.readFileSync(image.path);
    } catch (error) {
      throw new HostError('invalid', `read attached image: ${error.message}`);
    }
    if (data.length > MAX_IMAGE_BYTES) throw new HostError('invalid', 'attached image is larger than 5 MB');
    return { type: 'image', source: { type: 'base64', media_type: image.mediaType, data: data.toString('base64') } };
  });
  if (text !== '') blocks.push({ type: 'text', text });
  return blocks;
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
  child.stderr.on('data', (chunk) => {
    s.stderr = (s.stderr + chunk).slice(-4096);
  });
  child.stderr.on('error', () => {});
  // The SDK signals every Claude process it spawned when the host exits. These
  // outlive the host, so a running turn finishes after it.
  const kill = child.kill.bind(child);
  child.kill = (signal) => !exiting && kill(signal);
  child.on('exit', (code, signal) => exited({ code, signal }));
  child.on('error', (error) => exited({ code: null, signal: null, error: error.message }));
  options.signal?.addEventListener('abort', () => {
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM');
  });
  if (!child.pid) return child;
  s.pid = child.pid;
  s.started = processStart(s.pid);
  if (!s.started) {
    child.kill('SIGKILL');
    throw new Error(`cannot read the start time of Claude process ${s.pid}, so a later host could not tell it from a reused pid`);
  }
  writeState();
  emit(s.id, 'opened', { pid: s.pid, cwd: s.cwd });
  return child;
}

async function run(s, exit) {
  let error = '';
  try {
    for await (const message of s.query) {
      for (const uuid of message.user_message_uuids ?? []) s.owed.delete(uuid);
      if (message.type === 'result') s.measureAfter(message);
      if (message.type === 'system' && message.subtype === 'session_state_changed') {
        s.stateEvents = true;
        if (message.state === 'running') s.turnStarted();
        s.setState(message.state === 'idle' ? 'idle' : 'busy');
      } else if (message.type === 'system' && message.subtype === 'background_tasks_changed') {
        const tasks = Array.isArray(message.tasks) ? message.tasks : [];
        const live = new Set(tasks.map((task) => task.task_id));
        for (const task of tasks) if (task.ambient) s.ambient.add(task.task_id);
        for (const task of s.tasks) if (!task.ambient && !live.has(task.task_id) && s.state === 'idle') s.owe(`task:${task.task_id}`);
        s.tasks = tasks;
        for (const id of [...s.taskSince.keys()]) if (!live.has(id)) s.taskSince.delete(id);
        for (const id of live) if (!s.taskSince.has(id)) s.taskSince.set(id, new Date().toISOString());
        emit(s.id, 'tasks', { tasks: s.describeTasks() });
        s.retireIfDone();
      } else if (message.type === 'system' && message.subtype === 'task_notification') {
        // An ambient task is not activity, so its end owes no turn. Its
        // notification carries no flag; the live set said so while it ran.
        if (!s.ambient.delete(message.task_id) && s.state === 'idle') s.owe(`task:${message.task_id}`);
      } else if (message.type === 'result' && !s.stateEvents && s.inbox.length === 0) {
        s.guessesIdle = true;
        s.turnStarted();
        if (!stateEventsMissing) {
          stateEventsMissing = true;
          process.stderr.write(`claude-host: Claude ${s.pid} sent a turn result without session state events; idle is read from turn results, which miss turns Claude starts itself\n`);
        }
        s.setState('idle');
        s.retireIfDone({ turnEnded: true });
      }
      emit(s.id, 'sdk', { message });
    }
  } catch (e) {
    error = e?.message ?? String(e);
  }
  s.ended = true;
  for (const pending of [...s.pending.values()]) pending.settle('ended', { behavior: 'deny', message: WITHDRAWN });
  const status = await Promise.race([exit, timeout(10_000, 'exit').catch(() => ({ code: null, signal: null }))]);
  sessions.delete(s.id);
  writeState();
  emit(s.id, 'exited', { code: status.code, signal: status.signal, error: error || status.error || '', stderr: s.stderr.slice(-1024) });
  exitWhenDrained();
}

function session(id) {
  const s = sessions.get(id);
  if (!s || s.ended) {
    refuseOrphaned(id);
    throw new HostError('not_open', `Claude session ${id} is not open in the host`);
  }
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

// A client may hang up before its reply is written. The write then fails
// with EPIPE, which the socket reports and readline repeats on its interface;
// both are that client's loss, not the host's, so neither may end the host.
function serve(socket) {
  socket.on('error', () => {});
  socket.on('close', () => subscribers.delete(socket));
  const lines = readline.createInterface({ input: socket, crlfDelay: Infinity });
  lines.on('error', () => {});
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

// settings keeps the permission mode and model each conversation last ran
// with here, so a resume reopens in them. It is bounded to the most recent.
function loadSettings(settingsPath) {
  try {
    const state = JSON.parse(fs.readFileSync(settingsPath, 'utf8'));
    return new Map(Object.entries(state.sessions ?? {}));
  } catch {
    return new Map();
  }
}

function writeSettings() {
  const entries = [...settings.entries()].sort((a, b) => String(b[1].updatedAt).localeCompare(String(a[1].updatedAt)));
  for (const [id] of entries.splice(MAX_SETTINGS)) settings.delete(id);
  const target = path.join(args.state, 'settings.json');
  const temp = `${target}.${process.pid}.tmp`;
  fs.writeFileSync(temp, JSON.stringify({ sessions: Object.fromEntries(entries) }) + '\n', { mode: 0o600 });
  fs.renameSync(temp, target);
}

function refuseOrphaned(id) {
  const orphan = liveOrphans().find((o) => o.sessionId === id);
  if (orphan) throw new HostError('held', `Claude process ${orphan.pid} from an earlier host still runs this session`);
}

function liveOrphans() {
  for (const [id, orphan] of orphans) if (!runs(orphan)) orphans.delete(id);
  return [...orphans.values()];
}

// A retiring host stops recording: state.json belongs to the host that
// replaced it, which holds this host's sessions as running processes.
function writeState() {
  if (draining) return;
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
  const ps = spawnSync('ps', ['-o', 'lstart=', '-p', String(pid)], { encoding: 'utf8', env: { ...process.env, LC_ALL: 'C', TZ: 'UTC0' } });
  return ps.status === 0 ? ps.stdout.trim() : '';
}

// killSession kills a session's Claude process and the processes it started
// in its group, such as background shells, if Claude still runs; its start
// time tells it from a later process given the same pid. Claude is spawned
// detached, so it leads its own process group.
function killSession(s) {
  if (!runs({ pid: s.pid, started: s.started })) return;
  try {
    process.kill(-s.pid, 'SIGKILL');
  } catch {
    try {
      process.kill(s.pid, 'SIGKILL');
    } catch {}
  }
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
    fs.unlinkSync(listening);
  } catch {}
  process.exit(code);
}

// retire hands the daemon's socket and the host lock to the next host while
// this one keeps serving its own sessions on a socket of its own, named by its
// pid beside the daemon's, until the last one closes. It refuses new sessions.
async function retire() {
  const socket = `${args.socket.replace(/\.sock$/, '')}.${process.pid}.sock`;
  const next = net.createServer(serve);
  next.on('error', (error) => process.stderr.write(`claude-host: ${error.message}\n`));
  try {
    fs.unlinkSync(socket);
  } catch {}
  await new Promise((resolve, reject) => {
    next.once('error', reject);
    next.listen(socket, resolve);
  });
  fs.chmodSync(socket, 0o600);
  draining = true;
  server.close();
  try {
    fs.unlinkSync(args.socket);
  } catch {}
  server = next;
  listening = socket;
  // A subscriber came in through the daemon's socket and follows whichever
  // host serves it. It gets no more events from this host and is closed once
  // any reply it waits on is written, so it reconnects to the new host, and
  // follows this one on its own socket if it still wants to.
  const following = [...subscribers];
  subscribers.clear();
  setImmediate(() => {
    for (const subscriber of following) subscriber.end();
  });
  fs.closeSync(LOCK_FD);
  process.stderr.write(`claude-host: pid ${process.pid} retiring with ${sessions.size} sessions on ${socket}\n`);
}

function fail(message) {
  process.stderr.write(`claude-host: ${message}\n`);
  process.exit(2);
}

try {
  fs.unlinkSync(args.socket);
} catch {}
let server = net.createServer(serve);
// A host that cannot listen exits with the reason. Once listening, a failed
// accept costs that one client, not the sessions this host runs.
server.on('error', (error) => {
  if (!server.listening) fail(`cannot listen on ${args.socket}: ${error.message}`);
  process.stderr.write(`claude-host: ${error.message}\n`);
});
server.listen(args.socket, () => {
  fs.chmodSync(args.socket, 0o600);
  writeState();
  process.stderr.write(`claude-host: pid ${process.pid} protocol ${PROTOCOL} version ${args.version} listening on ${args.socket}\n`);
});
process.on('exit', () => {
  exiting = true;
});
process.on('SIGTERM', () => shutdown(0));
process.on('SIGINT', () => shutdown(0));
// stderr is host.log. A log that cannot be written loses lines, not sessions.
process.stderr.on('error', () => {});
