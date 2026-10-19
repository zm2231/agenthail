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

test('open, send and stream a turn bound to the message uuid', async () => {
  const f = fixture();
  await startHost(f, { version: '1.2.3' });
  const c = new Client(f.socket);
  assert.deepEqual(await c.call('hello'), { protocol: 1, hostVersion: '1.2.3', pid: (await c.call('hello')).pid });
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

test('bypassPermissions opens with explicit consent to skip permissions', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd, permissionMode: 'bypassPermissions' });
  const spawnRecord = JSON.parse(fs.readFileSync(path.join(f.claudeDir, 'spawns.jsonl'), 'utf8').trim());
  assert.ok(spawnRecord.argv.some((a) => a.includes('bypassPermissions')), spawnRecord.argv.join(' '));
  assert.ok(spawnRecord.argv.includes('--allow-dangerously-skip-permissions'), spawnRecord.argv.join(' '));
  assert.ok(!spawnRecord.argv.some((a) => a.startsWith('--permission-prompt-tool')), spawnRecord.argv.join(' '));
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

test('every permission request is answered with a denial and an event', async () => {
  const f = fixture();
  await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const id = randomUUID();
  await c.call('open', { sessionId: id, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: id, uuid, text: 'TOOL' });
  await c.event(turnResult(id, uuid));
  const denied = await c.event((e) => e.kind === 'permission_denied');
  assert.equal(denied.tool, 'Bash');
  assert.equal(denied.toolUseId, 'toolu_fake');
  assert.match(transcript(f, id)[0].reply, /^tool:deny:Agenthail cannot answer permission prompts/);
  c.close();
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
  assert.equal((await new Client(f.socket).call('hello')).protocol, 1);
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

test('drain closes sessions once idle and exits the host', async () => {
  const f = fixture();
  const host = await startHost(f);
  const c = new Client(f.socket);
  await c.call('subscribe');
  const idle = randomUUID();
  const busy = randomUUID();
  await c.call('open', { sessionId: idle, cwd: f.cwd });
  await c.call('open', { sessionId: busy, cwd: f.cwd });
  const uuid = randomUUID();
  await c.call('send', { sessionId: busy, uuid, text: 'SLOW 500' });
  assert.deepEqual(await c.call('drain'), { sessions: 2 });
  await assert.rejects(c.call('open', { sessionId: randomUUID(), cwd: f.cwd }), { code: 'draining' });
  await assert.rejects(c.call('send', { sessionId: busy, uuid: randomUUID(), text: 'late' }), { code: 'draining' });
  assert.equal(await host.exited, 0);
  assert.equal(transcript(f, busy)[0].reply, 'slow:500');
  assert.ok(!fs.existsSync(f.socket));
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
