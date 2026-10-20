import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import { after, test } from 'node:test';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const hostScript = path.join(here, '..', 'host.mjs');
const fakeClaude = path.join(here, 'fake-claude.mjs');
const hosts = [];

after(() => {
  for (const host of hosts) host.kill('SIGKILL');
});

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ch-'));
  const state = path.join(root, 'state');
  const claudeDir = path.join(root, 'claude');
  const cwd = path.join(root, 'work');
  for (const dir of [state, claudeDir, cwd]) fs.mkdirSync(dir);
  fs.writeFileSync(path.join(state, 'lock'), '');
  return { root, state, claudeDir, cwd, socket: path.join(root, 'h.sock') };
}

async function startHost(f, { lock = true, version = 'test', env = {}, claude = fakeClaude } = {}) {
  const fd = lock ? fs.openSync(path.join(f.state, 'lock'), 'r') : null;
  const host = spawn(process.execPath, [hostScript, '--socket', f.socket, '--state', f.state, '--claude', claude, '--version', version], {
    env: { ...process.env, FAKE_CLAUDE_DIR: f.claudeDir, ...env },
    stdio: lock ? ['ignore', 'ignore', 'pipe', fd] : ['ignore', 'ignore', 'pipe'],
  });
  if (lock) fs.closeSync(fd);
  hosts.push(host);
  host.stderrText = '';
  host.stderr.on('data', (chunk) => (host.stderrText += chunk));
  host.exited = new Promise((resolve) => host.on('exit', (code) => resolve(code)));
  if (!lock) return host;
  for (let i = 0; i < 200 && !host.stderrText.includes('listening'); i++) await sleep(25);
  return host;
}

class Client {
  constructor(socket) {
    this.next = 0;
    this.waiting = new Map();
    this.events = [];
    this.eventWaiters = [];
    this.conn = net.createConnection(socket);
    readline.createInterface({ input: this.conn }).on('line', (line) => {
      const message = JSON.parse(line);
      if (message.event) {
        this.events.push(message.event);
        for (const waiter of [...this.eventWaiters]) waiter();
        return;
      }
      const pending = this.waiting.get(message.id);
      this.waiting.delete(message.id);
      if (message.error) pending.reject(Object.assign(new Error(message.error.message), { code: message.error.code }));
      else pending.resolve(message.result);
    });
    this.ready = new Promise((resolve, reject) => {
      this.conn.once('connect', resolve);
      this.conn.once('error', reject);
    });
  }

  async call(method, params = {}) {
    await this.ready;
    const id = ++this.next;
    const result = new Promise((resolve, reject) => this.waiting.set(id, { resolve, reject }));
    this.conn.write(JSON.stringify({ id, method, params }) + '\n');
    return result;
  }

  async event(match, ms = 10000) {
    const deadline = Date.now() + ms;
    for (;;) {
      const found = this.events.find(match);
      if (found) return found;
      if (Date.now() > deadline) throw new Error(`no matching event in ${JSON.stringify(this.events.map((e) => e.kind))}`);
      await new Promise((resolve) => {
        const waiter = () => {
          this.eventWaiters = this.eventWaiters.filter((w) => w !== waiter);
          resolve();
        };
        this.eventWaiters.push(waiter);
        setTimeout(waiter, 100);
      });
    }
  }

  close() {
    this.conn.destroy();
  }
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const transcript = (f, id) =>
  fs.existsSync(path.join(f.claudeDir, `${id}.jsonl`))
    ? fs.readFileSync(path.join(f.claudeDir, `${id}.jsonl`), 'utf8').split('\n').filter(Boolean).map((l) => JSON.parse(l))
    : [];
const turnResult = (sessionId, uuid) => (e) => e.sessionId === sessionId && e.kind === 'sdk' && e.message.type === 'result' && e.message.user_message_uuids?.includes(uuid);
const alive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
};

test('a host started without the daemon lock exits', async () => {
  const f = fixture();
  const host = await startHost(f, { lock: false });
  assert.equal(await host.exited, 2);
  assert.match(host.stderrText, /runs only under the Agenthail daemon/);
  assert.ok(!fs.existsSync(f.socket));
});

test('a host that cannot listen exits with the reason', async () => {
  const f = fixture();
  f.socket = path.join(f.root, 'missing', 'h.sock');
  const host = await startHost(f);
  assert.equal(await host.exited, 2);
  assert.match(host.stderrText, /cannot listen on .*missing/);
});

test('open, send and stream a turn bound to the message uuid', async () => {
  const f = fixture();
  await startHost(f, { version: '1.2.3' });
  const c = new Client(f.socket);
  assert.deepEqual(await c.call('hello'), { protocol: 2, hostVersion: '1.2.3', pid: (await c.call('hello')).pid });
  await c.call('subscribe', { after: 0 });
  const id = randomUUID();
  const opened = await c.call('open', { sessionId: id, cwd: f.cwd, permissionMode: 'acceptEdits', model: 'opus', effort: 'high', agent: 'reviewer', name: 'Review the parser' });
  assert.equal(opened.sessionId, id);
  assert.equal(opened.state, 'idle');
  assert.ok(opened.pid > 0);
  const spawnRecord = JSON.parse(fs.readFileSync(path.join(f.claudeDir, 'spawns.jsonl'), 'utf8').trim());
  assert.ok(spawnRecord.argv.includes(`--session-id=${id}`) || spawnRecord.argv.includes(id));
  assert.ok(spawnRecord.argv.some((a) => a.includes('acceptEdits')));
  for (const [flag, value] of [['--model', 'opus'], ['--effort', 'high'], ['--agent', 'reviewer'], ['--name', 'Review the parser']]) {
    const at = spawnRecord.argv.indexOf(flag);
    const passed = spawnRecord.argv.includes(`${flag}=${value}`) || (at >= 0 && spawnRecord.argv[at + 1] === value);
    assert.ok(passed, `${flag} ${value} in ${spawnRecord.argv.join(' ')}`);
  }
  assert.equal(spawnRecord.cwd, fs.realpathSync(f.cwd));

  const uuid = randomUUID();
  assert.deepEqual(await c.call('send', { sessionId: id, uuid, text: 'hello', origin: { kind: 'human' } }), { accepted: true, duplicate: false });
  await c.event(turnResult(id, uuid));
  const [line] = transcript(f, id);
  assert.equal(line.uuid, uuid);
  assert.equal(line.reply, 'echo:hello');
  assert.deepEqual(line.origin, { kind: 'human' });
  assert.ok(c.events.some((e) => e.kind === 'state' && e.state === 'busy'));
  const seqs = c.events.map((e) => e.seq);
  assert.deepEqual(seqs, [...seqs].sort((a, b) => a - b));
  c.close();
});

test('every session opens with consent to bypassPermissions and asks the host about tools', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  for (const permissionMode of ['bypassPermissions', 'acceptEdits']) {
    await c.call('open', { sessionId: randomUUID(), cwd: f.cwd, permissionMode });
  }
  const spawns = fs.readFileSync(path.join(f.claudeDir, 'spawns.jsonl'), 'utf8').trim().split('\n').map((l) => JSON.parse(l));
  assert.equal(spawns.length, 2);
  for (const [i, mode] of ['bypassPermissions', 'acceptEdits'].entries()) {
    const argv = spawns[i].argv;
    assert.ok(argv.some((a) => a.includes(mode)), argv.join(' '));
    assert.ok(argv.includes('--allow-dangerously-skip-permissions'), argv.join(' '));
    assert.ok(argv.includes('--permission-prompt-tool=stdio'), argv.join(' '));
  }
  c.close();
});

test('a session opened in a Claude config root runs its Claude there', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  const root = path.join(f.cwd, 'claude-work');
  const resumed = randomUUID();
  await c.call('open', { resume: resumed, cwd: f.cwd, configDir: root });
  await c.call('open', { sessionId: randomUUID(), cwd: f.cwd });
  const [inRoot, primary] = fs.readFileSync(path.join(f.claudeDir, 'spawns.jsonl'), 'utf8').trim().split('\n').map((l) => JSON.parse(l));
  assert.equal(inRoot.sessionId, resumed);
  assert.equal(inRoot.configDir, root);
  assert.equal(primary.configDir, process.env.CLAUDE_CONFIG_DIR ?? null);
  c.close();
});

test('a peer message keeps its text and carries the sender as origin', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  const text = '<cross-session-message from="x">hi</cross-session-message>';
  await c.call('send', { sessionId: id, uuid, text, origin: { kind: 'peer', from: 'claude:abc', name: 'sender' }, priority: 'next' });
  await c.event(turnResult(id, uuid));
  const [line] = transcript(f, id);
  assert.equal(line.text, text);
  assert.deepEqual(line.origin, { kind: 'peer', from: 'claude:abc', name: 'sender' });
  assert.equal(line.priority, 'next');
  await assert.rejects(c.call('send', { sessionId: id, uuid: randomUUID(), text: 'x', origin: { kind: 'robot' } }), { code: 'invalid' });
  c.close();
});

test('a duplicate uuid runs once', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text: 'once' });
  assert.deepEqual(await c.call('send', { sessionId: id, uuid, text: 'once' }), { accepted: true, duplicate: true });
  await c.event(turnResult(id, uuid));
  const second = randomUUID();
  await c.call('send', { sessionId: id, uuid: second, text: 'after' });
  await c.event(turnResult(id, second));
  assert.deepEqual(transcript(f, id).map((l) => l.text), ['once', 'after']);
  c.close();
});

async function openPending(f, c, text) {
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text });
  const opened = await c.event((e) => e.kind === 'request_opened' && e.sessionId === id);
  return { id, uuid, request: opened.request };
}

test('a permission request waits in the listing until a person allows it', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, uuid, request } = await openPending(f, c, 'TOOL');
  assert.equal(request.tool, 'Bash');
  assert.equal(request.kind, 'approval');
  assert.equal(request.toolUseId, 'toolu_fake');
  assert.equal(request.title, 'Claude wants to run true');
  assert.deepEqual(request.input, { command: 'true' });
  assert.equal(request.alwaysAllow, false);
  await sleep(300);
  const listed = (await c.call('list')).sessions.find((s) => s.sessionId === id);
  assert.equal(listed.state, 'busy');
  assert.deepEqual(listed.requests.map((r) => r.requestId), [request.requestId]);
  await assert.rejects(c.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow', always: true } }), { code: 'invalid' });
  assert.deepEqual(await c.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow' } }), { outcome: 'allowed' });
  await c.event(turnResult(id, uuid));
  assert.equal(transcript(f, id)[0].reply, 'tool:allow:');
  assert.equal((await c.event((e) => e.kind === 'request_resolved')).outcome, 'allowed');
  assert.deepEqual((await c.call('list')).sessions.find((s) => s.sessionId === id).requests, []);
  await assert.rejects(c.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow' } }), { code: 'stale' });
  c.close();
});

test('a denial carries its reason and always-allow applies the suggested rule', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const denied = await openPending(f, c, 'TOOL');
  await c.call('respond', { sessionId: denied.id, requestId: denied.request.requestId, decision: { behavior: 'deny', message: 'not now' } });
  await c.event(turnResult(denied.id, denied.uuid));
  assert.equal(transcript(f, denied.id)[0].reply, 'tool:deny:not now');

  const always = await openPending(f, c, 'TOOL ALWAYS');
  assert.equal(always.request.alwaysAllow, true);
  assert.deepEqual(await c.call('respond', { sessionId: always.id, requestId: always.request.requestId, decision: { behavior: 'allow', always: true } }), { outcome: 'allowed_always' });
  await c.event(turnResult(always.id, always.uuid));
  assert.equal(transcript(f, always.id)[0].reply, 'tool:allow::always');
  c.close();
});

test('a question is answered with an answer to each of its questions', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, uuid, request } = await openPending(f, c, 'QUESTION');
  assert.equal(request.kind, 'question');
  assert.deepEqual(request.questions, [{ question: 'Which color?', header: 'Color', multiSelect: false, options: [{ label: 'Red', description: 'Warm' }, { label: 'Blue', description: 'Cool' }] }]);
  const respond = (decision) => c.call('respond', { sessionId: id, requestId: request.requestId, decision });
  await assert.rejects(respond({ behavior: 'allow' }), { code: 'invalid' });
  await assert.rejects(respond({ behavior: 'allow', answers: {} }), { code: 'invalid' });
  await assert.rejects(respond({ behavior: 'allow', answers: { 'Which color?': 'Blue', 'Which size?': 'Big' } }), { code: 'invalid' });
  assert.deepEqual(await respond({ behavior: 'allow', answers: { 'Which color?': 'Blue' } }), { outcome: 'answered' });
  await c.event(turnResult(id, uuid));
  assert.equal(transcript(f, id)[0].reply, 'question:allow:{"Which color?":"Blue"}');
  c.close();
});

test('a request answered for another session or one never asked is refused', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, request } = await openPending(f, c, 'TOOL');
  const other = randomUUID();
  await c.call('open', { sessionId: other, cwd: f.cwd });
  await assert.rejects(c.call('respond', { sessionId: other, requestId: request.requestId, decision: { behavior: 'allow' } }), { code: 'stale' });
  await assert.rejects(c.call('respond', { sessionId: randomUUID(), requestId: request.requestId, decision: { behavior: 'allow' } }), { code: 'not_open' });
  await assert.rejects(c.call('respond', { sessionId: id, requestId: randomUUID(), decision: { behavior: 'allow' } }), { code: 'stale' });
  await assert.rejects(c.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'maybe' } }), { code: 'invalid' });
  assert.equal((await c.call('list')).sessions.find((s) => s.sessionId === id).requests.length, 1);
  c.close();
});

test('an interrupt withdraws an open request', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, uuid, request } = await openPending(f, c, 'TOOL');
  await c.call('interrupt', { sessionId: id });
  assert.equal((await c.event((e) => e.kind === 'request_resolved' && e.requestId === request.requestId)).outcome, 'withdrawn');
  await c.event(turnResult(id, uuid));
  await assert.rejects(c.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow' } }), { code: 'stale' });
  c.close();
});

test('a request does not outlive its host', async () => {
  const f = fixture();
  const first = await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, request } = await openPending(f, c, 'TOOL');
  const { pid } = (await c.call('list')).sessions.find((s) => s.sessionId === id);
  first.kill('SIGKILL');
  await first.exited;
  c.close();
  for (let i = 0; i < 100 && alive(pid); i++) await sleep(50);
  assert.ok(!alive(pid), 'Claude exits once nobody can answer its prompt');
  await startHost(f);
  const d = new Client(f.socket);
  assert.deepEqual((await d.call('list')).sessions, []);
  await d.call('open', { resume: id, cwd: f.cwd });
  await assert.rejects(d.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow' } }), { code: 'stale' });
  d.close();
});

test('model and permission mode change live and a resume keeps them', async () => {
  const f = fixture();
  const first = await startHost(f);
  const c = new Client(f.socket);
  const id = randomUUID();
  const opened = await c.call('open', { sessionId: id, cwd: f.cwd, permissionMode: 'acceptEdits' });
  assert.equal(opened.permissionMode, 'acceptEdits');
  assert.equal((await c.call('setPermissionMode', { sessionId: id, permissionMode: 'bypassPermissions' })).permissionMode, 'bypassPermissions');
  assert.equal((await c.call('setModel', { sessionId: id, model: 'opus' })).model, 'opus');
  await assert.rejects(c.call('setPermissionMode', { sessionId: id, permissionMode: 'yolo' }), { code: 'invalid' });
  await assert.rejects(c.call('setModel', { sessionId: id, model: '' }), { code: 'invalid' });
  await assert.rejects(c.call('setModel', { sessionId: randomUUID(), model: 'opus' }), { code: 'not_open' });
  const controls = fs.readFileSync(path.join(f.claudeDir, 'controls.jsonl'), 'utf8').trim().split('\n').map((l) => JSON.parse(l));
  assert.deepEqual(controls.map((r) => [r.subtype, r.mode ?? r.model]), [['set_permission_mode', 'bypassPermissions'], ['set_model', 'opus']]);
  await c.call('close', { sessionId: id });
  c.close();
  first.kill('SIGKILL');
  await first.exited;

  await startHost(f);
  const d = new Client(f.socket);
  const resumed = await d.call('open', { resume: id, cwd: f.cwd });
  assert.equal(resumed.permissionMode, 'bypassPermissions');
  assert.equal(resumed.model, 'opus');
  const argv = fs.readFileSync(path.join(f.claudeDir, 'spawns.jsonl'), 'utf8').trim().split('\n').map((l) => JSON.parse(l)).at(-1).argv;
  assert.ok(argv.includes('--permission-mode=bypassPermissions') && argv.includes('--model=opus'), argv.join(' '));
  d.close();
});

test('interrupt ends the running turn', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text: 'SLOW 30000' });
  await c.event((e) => e.kind === 'state' && e.state === 'busy');
  await sleep(200);
  await c.call('interrupt', { sessionId: id });
  await c.event(turnResult(id, uuid));
  assert.equal(transcript(f, id)[0].reply, 'interrupted');
  c.close();
});

test('a host restart keeps the running turn and resumes the session afterwards', async () => {
  const f = fixture();
  const first = await startHost(f);
  const c = new Client(f.socket);
  const id = randomUUID();
  const { pid } = await c.call('open', { sessionId: id, cwd: f.cwd });
  const running = randomUUID();
  await c.call('send', { sessionId: id, uuid: running, text: 'SLOW 1500' });
  await sleep(300);
  first.kill('SIGKILL');
  await first.exited;
  c.close();
  assert.ok(alive(pid), 'the CLI outlives the host');

  await startHost(f, { env: { TZ: 'Pacific/Auckland' } });
  const d = new Client(f.socket);
  await d.call('subscribe');
  const listed = await d.call('list');
  assert.deepEqual(listed.sessions, []);
  assert.deepEqual(listed.orphans.map((o) => [o.sessionId, o.pid]), [[id, pid]]);
  await assert.rejects(d.call('open', { resume: id, cwd: f.cwd }), { code: 'held' });
  await assert.rejects(d.call('send', { sessionId: id, uuid: randomUUID(), text: 'early' }), { code: 'held' });

  for (let i = 0; i < 100 && alive(pid); i++) await sleep(50);
  assert.ok(!alive(pid), 'the orphan exits after its turn');
  assert.deepEqual(transcript(f, id).map((l) => [l.uuid, l.reply, l.inputClosed]), [[running, 'slow:1500', true]]);
  assert.deepEqual((await d.call('list')).orphans, []);

  await d.call('open', { resume: id, cwd: f.cwd });
  await d.call('send', { sessionId: id, uuid: running, text: 'SLOW 1500' });
  const next = randomUUID();
  await d.call('send', { sessionId: id, uuid: next, text: 'next' });
  await d.event(turnResult(id, next));
  assert.deepEqual(transcript(f, id).map((l) => l.uuid), [running, next]);
  d.close();
});

test('a host stopped by a signal leaves the running turn to finish', async () => {
  const f = fixture();
  const first = await startHost(f);
  const c = new Client(f.socket);
  const id = randomUUID();
  const { pid } = await c.call('open', { sessionId: id, cwd: f.cwd });
  const running = randomUUID();
  await c.call('send', { sessionId: id, uuid: running, text: 'SLOW 1500' });
  await sleep(300);
  first.kill('SIGTERM');
  await first.exited;
  c.close();
  assert.ok(alive(pid), 'the CLI outlives the host');
  for (let i = 0; i < 100 && alive(pid); i++) await sleep(50);
  assert.deepEqual(transcript(f, id).map((l) => [l.uuid, l.reply]), [[running, 'slow:1500']]);
});

test('a recorded pid now used by another process is not an orphan', async () => {
  const f = fixture();
  const other = spawn('sleep', ['30'], { stdio: 'ignore' });
  after(() => other.kill('SIGKILL'));
  const id = randomUUID();
  fs.writeFileSync(path.join(f.state, 'state.json'), JSON.stringify({ sessions: { [id]: { pid: other.pid, started: 'Thu Jan  1 00:00:00 1970', cwd: f.cwd } } }));
  await startHost(f);
  const c = new Client(f.socket);
  assert.deepEqual((await c.call('list')).orphans, []);
  await c.call('open', { resume: id, cwd: f.cwd });
  c.close();
});

test('a session whose process start time cannot be read does not open', async () => {
  const f = fixture();
  const bin = path.join(f.root, 'bin');
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, 'ps'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  await startHost(f, { env: { PATH: `${bin}:${process.env.PATH}` } });
  const c = new Client(f.socket);
  const id = randomUUID();
  const started = Date.now();
  await assert.rejects(c.call('open', { sessionId: id, cwd: f.cwd }), { code: 'open_failed' });
  assert.ok(Date.now() - started < 5000, 'the open fails promptly');
  assert.deepEqual((await c.call('list')).sessions, []);
  const running = () => spawnSync('pgrep', ['-f', id]).status === 0;
  for (let i = 0; i < 40 && running(); i++) await sleep(25);
  assert.ok(!running(), 'the Claude process is stopped');
  c.close();
});

test('a Claude that cannot be started fails the open and leaves the host running', async () => {
  const f = fixture();
  const claude = path.join(f.root, 'claude-bin');
  fs.writeFileSync(claude, '', { mode: 0o644 });
  const host = await startHost(f, { claude });
  const c = new Client(f.socket);
  await assert.rejects(c.call('open', { sessionId: randomUUID(), cwd: f.cwd }), { code: 'open_failed' });
  await sleep(200);
  assert.equal(host.exitCode, null, host.stderrText);
  assert.equal((await new Client(f.socket).call('hello')).protocol, 2);
  c.close();
});

test('a Claude that exits with an error before it starts refuses the open with its output', async () => {
  const f = fixture();
  const id = randomUUID();
  fs.writeFileSync(path.join(f.claudeDir, `refuse-${id}`), 'Workspace not trusted');
  await startHost(f);
  const c = new Client(f.socket);
  await assert.rejects(c.call('open', { resume: id, cwd: f.cwd }), (error) => error.code === 'refused' && error.message.includes('Workspace not trusted'));
  assert.deepEqual((await c.call('list')).sessions, []);
  c.close();
});

const retiringSocket = (f, host) => f.socket.replace(/\.sock$/, `.${host.pid}.sock`);

// takeLock reports whether another host could start now: a second host is
// started with the lock, which it can hold only once the first let it go.
async function lockReleased(f) {
  const probe = { ...f, socket: path.join(f.root, 'probe.sock') };
  const host = await startHost(probe);
  const ok = host.exitCode === null && host.stderrText.includes('listening');
  host.kill('SIGKILL');
  await host.exited;
  return ok;
}

test('drain retires the host: it gives up the socket and lock and serves its sessions until each is idle', async () => {
  const f = fixture();
  const host = await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const idle = randomUUID();
  const busy = randomUUID();
  const idleSession = await c.call('open', { sessionId: idle, cwd: f.cwd });
  await c.call('open', { sessionId: busy, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: busy, uuid, text: 'SLOW 800' });
  await c.event((e) => e.sessionId === busy && e.kind === 'state' && e.state === 'busy');
  assert.equal((await c.call('drain')).sessions >= 1, true);
  assert.ok(!fs.existsSync(f.socket), 'the daemon socket is free for the next host');
  assert.ok(await lockReleased(f), 'the lock is free for the next host');
  const r = new Client(retiringSocket(f, host));
  assert.equal((await r.call('hello')).pid, host.pid);
  await assert.rejects(r.call('open', { sessionId: randomUUID(), cwd: f.cwd }), { code: 'draining' });
  for (let i = 0; i < 100 && alive(idleSession.pid); i++) await sleep(50);
  assert.ok(!alive(idleSession.pid), 'an idle session closes at once');
  const late = randomUUID();
  assert.deepEqual(await r.call('send', { sessionId: busy, uuid: late, text: 'late' }), { accepted: true, duplicate: false });
  assert.equal(await host.exited, 0);
  assert.deepEqual(transcript(f, busy).map((l) => l.reply), ['slow:800', 'echo:late']);
  assert.ok(!fs.existsSync(retiringSocket(f, host)));
  c.close();
  r.close();
});

test('retiring lets go of subscribers that came in through the daemon socket', async () => {
  const f = fixture();
  const host = await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const closed = new Promise((resolve) => c.conn.once('close', resolve));
  const d = new Client(f.socket);
  const id = randomUUID();
  await d.call('open', { sessionId: id, cwd: f.cwd });
  await d.call('send', { sessionId: id, uuid: randomUUID(), text: 'SLOW 1000' });
  await d.call('drain');
  await closed;
  const r = new Client(retiringSocket(f, host));
  await r.call('subscribe', { after: 0 });
  await r.event((e) => e.sessionId === id && e.kind === 'exited');
  d.close();
  r.close();
});

test('a retiring host keeps an open request answerable and closes the session only after its turn', async () => {
  const f = fixture();
  const host = await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, uuid, request } = await openPending(f, c, 'TOOL');
  await c.call('drain');
  await sleep(300);
  const r = new Client(retiringSocket(f, host));
  const listed = (await r.call('list')).sessions.find((s) => s.sessionId === id);
  assert.equal(listed.state, 'busy');
  assert.deepEqual(listed.requests.map((q) => q.requestId), [request.requestId]);
  assert.deepEqual(await r.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow' } }), { outcome: 'allowed' });
  assert.equal(await host.exited, 0);
  assert.deepEqual(transcript(f, id).map((l) => [l.uuid, l.reply]), [[uuid, 'tool:allow:']]);
  c.close();
  r.close();
});

test('a retiring host does not close an idle session while a background task runs', async () => {
  const f = fixture();
  const host = await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const id = randomUUID();
  const { pid } = await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text: 'BACKGROUND 1500' });
  await c.event(turnResult(id, uuid));
  await c.event((e) => e.sessionId === id && e.kind === 'state' && e.state === 'idle');
  const started = Date.now();
  await c.call('drain');
  await sleep(700);
  assert.ok(alive(pid), 'the session runs while its background task does');
  assert.equal(host.exitCode, null);
  assert.equal(await host.exited, 0);
  assert.ok(Date.now() - started >= 1000, 'the host waited for the background task');
  assert.ok(!alive(pid));
  c.close();
});

test('a Claude without session state events falls back to turn results and says so', async () => {
  const f = fixture();
  const host = await startHost(f, { env: { FAKE_CLAUDE_NO_STATE_EVENTS: '1' } });
  const c = new Client(f.socket);
  await c.call('subscribe');
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text: 'SLOW 600' });
  await c.call('drain');
  await sleep(200);
  assert.equal(host.exitCode, null, 'a busy session is not closed');
  assert.equal(await host.exited, 0);
  assert.match(host.stderrText, /without session state events/);
  assert.deepEqual(transcript(f, id).map((l) => l.reply), ['slow:600']);
  c.close();
});

test('closing a session ends its open requests instead of leaving them listed', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const { id, request } = await openPending(f, c, 'TOOL');
  await c.call('close', { sessionId: id });
  assert.equal((await c.event((e) => e.kind === 'request_resolved' && e.requestId === request.requestId)).outcome, 'ended');
  assert.deepEqual((await c.call('list')).sessions, []);
  await assert.rejects(c.call('respond', { sessionId: id, requestId: request.requestId, decision: { behavior: 'allow' } }), { code: 'not_open' });
  c.close();
});

test('close ends the session and a send to it is refused', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  const id = randomUUID();
  const { pid } = await c.call('open', { sessionId: id, cwd: f.cwd });
  await c.call('close', { sessionId: id });
  for (let i = 0; i < 40 && alive(pid); i++) await sleep(50);
  assert.ok(!alive(pid));
  await assert.rejects(c.call('send', { sessionId: id, uuid: randomUUID(), text: 'x' }), { code: 'not_open' });
  assert.deepEqual((await c.call('list')).sessions, []);
  c.close();
});

test('a client that hangs up before its reply leaves the host running', async () => {
  const f = fixture();
  const host = await startHost(f);
  for (let i = 0; i < 200; i++) {
    const conn = net.createConnection(f.socket);
    const connected = await new Promise((resolve) => {
      conn.once('connect', () => resolve(true));
      conn.once('error', () => resolve(false));
    });
    if (!connected) break;
    conn.end(JSON.stringify({ id: 1, method: 'hello' }) + '\n');
    conn.destroy();
  }
  await sleep(200);
  assert.equal(host.exitCode, null, host.stderrText);
  const client = new Client(f.socket);
  assert.equal((await client.call('hello')).pid, host.pid);
  client.close();
});

test('open rejects a bad request', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await assert.rejects(c.call('open', { cwd: 'relative' }), { code: 'invalid' });
  await assert.rejects(c.call('open', { cwd: f.cwd, sessionId: 'nope' }), { code: 'invalid' });
  await assert.rejects(c.call('open', { cwd: f.cwd, permissionMode: 'yolo' }), { code: 'invalid' });
  await assert.rejects(c.call('open', { cwd: f.cwd, effort: 'extreme' }), { code: 'invalid' });
  await assert.rejects(c.call('open', { cwd: f.cwd, resume: randomUUID(), name: 'renamed' }), { code: 'invalid' });
  await assert.rejects(c.call('open', { cwd: f.cwd, configDir: 'relative/claude' }), { code: 'invalid' });
  await assert.rejects(c.call('nope'), { code: 'invalid' });
  c.close();
});

test('a send with images gives Claude image blocks before the text', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe', { after: 0 });
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=', 'base64');
  const image = path.join(f.cwd, 'shot.png');
  fs.writeFileSync(image, png);
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text: 'what is this?', images: [{ path: image, mediaType: 'image/png' }], origin: { kind: 'human' } });
  await c.event(turnResult(id, uuid));
  const blocks = [
    { type: 'image', source: { type: 'base64', media_type: 'image/png', data: png.toString('base64') } },
    { type: 'text', text: 'what is this?' },
  ];
  assert.equal(transcript(f, id)[0].reply, `echo:${JSON.stringify(blocks)}`);
  const imageOnly = randomUUID();
  await c.call('send', { sessionId: id, uuid: imageOnly, text: '', images: [{ path: image, mediaType: 'image/png' }] });
  await c.event(turnResult(id, imageOnly));
  assert.equal(transcript(f, id)[1].reply, `echo:${JSON.stringify(blocks.slice(0, 1))}`);
  await assert.rejects(c.call('send', { sessionId: id, uuid: randomUUID(), text: 'x', images: [{ path: 'shot.png', mediaType: 'image/png' }] }), { code: 'invalid' });
  await assert.rejects(c.call('send', { sessionId: id, uuid: randomUUID(), text: 'x', images: [{ path: image, mediaType: 'image/webp' }] }), { code: 'invalid' });
  await assert.rejects(c.call('send', { sessionId: id, uuid: randomUUID(), text: '' }), { code: 'invalid' });
  c.close();
});
