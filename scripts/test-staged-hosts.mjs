// Usage: node scripts/test-staged-hosts.mjs INSTALL_ROOT [--files-only]
import './test-home.mjs';
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';

const [root, mode] = process.argv.slice(2);
if (!root) fail('usage: test-staged-hosts.mjs INSTALL_ROOT [--files-only]');
const source = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const node = path.join(root, 'runtime', 'node', 'bin', 'node');
const SECRET = randomBytes(32).toString('hex');

for (const host of ['claude-host', 'browser-host', 'extension-host']) {
  const shipped = fs.readdirSync(path.join(source, host)).filter(name => name.endsWith('.mjs') || name.startsWith('package'));
  for (const name of shipped) {
    if (!fs.statSync(path.join(root, host, name), { throwIfNoEntry: false })?.isFile()) fail(`${host}/${name} is missing from ${root}`);
  }
  console.log(`${host}: ${shipped.join(', ')} staged`);
}
if (mode === '--files-only') process.exit(0);

const work = fs.mkdtempSync(path.join(os.tmpdir(), 'staged-'));
try {
  await claudeHost();
  await browserHost();
  await extensionHost();
} catch (error) {
  console.error(`error: ${error.message}`);
  process.exitCode = 1;
} finally {
  fs.rmSync(work, { recursive: true, force: true });
}

async function claudeHost() {
  const state = path.join(work, 'claude');
  fs.mkdirSync(state);
  fs.writeFileSync(path.join(state, 'lock'), '');
  const lock = fs.openSync(path.join(state, 'lock'), 'r');
  const socket = path.join(work, 'claude.sock');
  const child = start(['claude-host/host.mjs', '--socket', socket, '--state', state, '--claude', path.join(work, 'claude-is-not-run'), '--version', 'staged'], ['ignore', 'ignore', 'pipe', lock]);
  fs.closeSync(lock);
  const hello = await call(child, socket, 'hello');
  if (hello.pid !== child.pid || hello.hostVersion !== 'staged') fail(`claude-host hello: ${JSON.stringify(hello)}`);
  child.kill('SIGTERM');
  await exits(child, 'claude-host');
  console.log(`claude-host started from ${root} (protocol ${hello.protocol})`);
}

async function browserHost() {
  const socket = path.join(work, 'browser.sock');
  const child = start(['browser-host/host.mjs', '--socket', socket, '--browsers', path.join(work, 'browsers'), '--version', 'staged'], ['pipe', 'ignore', 'pipe']);
  child.stdin.write(SECRET + '\n');
  const hello = await call(child, socket, 'hello');
  if (hello.pid !== child.pid || hello.hostVersion !== 'staged') fail(`browser-host hello: ${JSON.stringify(hello)}`);
  const status = await call(child, socket, 'status');
  if (status.state !== 'absent' || !status.version) fail(`browser-host status: ${JSON.stringify(status)}`);
  child.stdin.end();
  await exits(child, 'browser-host');
  console.log(`browser-host started from ${root} (Chromium ${status.version} not downloaded)`);
}

async function extensionHost() {
  const socket = path.join(work, 'extension.sock');
  const child = start(['extension-host/host.mjs', '--socket', socket, '--store', path.join(work, 'extensions-data', 'store.db'), '--version', 'staged'], ['pipe', 'ignore', 'pipe']);
  child.stdin.write(SECRET + '\n');
  const hello = await call(child, socket, 'hello');
  if (hello.pid !== child.pid || hello.hostVersion !== 'staged') fail(`extension-host hello: ${JSON.stringify(hello)}`);
  const status = await call(child, socket, 'status');
  if (!Array.isArray(status.extensions)) fail(`extension-host status: ${JSON.stringify(status)}`);
  child.stdin.end();
  await exits(child, 'extension-host');
  console.log(`extension-host started from ${root} (protocol ${hello.protocol})`);
}

function start(args, stdio) {
  const child = spawn(node, [path.join(root, args[0]), ...args.slice(1)], { cwd: work, stdio, env: { PATH: '/usr/bin:/bin', HOME: work } });
  child.stderrText = '';
  child.stderr.on('data', chunk => { child.stderrText = (child.stderrText + chunk).slice(-4096); });
  child.exited = new Promise(resolve => child.on('exit', (code, signal) => resolve({ code, signal })));
  return child;
}

async function call(child, socket, method) {
  const deadline = Date.now() + 15_000;
  for (;;) {
    try {
      return await once(socket, method);
    } catch (error) {
      if (child.exitCode !== null || child.signalCode !== null) fail(`${method}: the host exited: ${child.stderrText}`);
      if (Date.now() > deadline) {
        child.kill('SIGKILL');
        fail(`${method}: no answer within 15s (${error.message}): ${child.stderrText}`);
      }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  }
}

function once(socket, method) {
  return new Promise((resolve, reject) => {
    const connection = net.connect(socket);
    connection.on('error', reject);
    connection.on('connect', () => {
      readline.createInterface({ input: connection }).once('line', line => {
        connection.destroy();
        const message = JSON.parse(line);
        if (message.error) reject(new Error(message.error.message));
        else resolve(message.result);
      });
      connection.write(JSON.stringify({ id: 1, method, params: {}, token: SECRET }) + '\n');
    });
  });
}

async function exits(child, name) {
  const timer = setTimeout(() => child.kill('SIGKILL'), 10_000);
  const { code, signal } = await child.exited;
  clearTimeout(timer);
  if (code !== 0) fail(`${name} exited with ${code ?? signal}: ${child.stderrText}`);
}

function fail(message) {
  throw new Error(message);
}
