// Protocol: docs/maintainers/browser-host.md. The first stdin line is the daemon's secret; exits when stdin closes.
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import readline from 'node:readline';
import { parseArgs } from 'node:util';
import { Browser } from './browser.mjs';
import { Output } from './output.mjs';
import { Tabs } from './tabs.mjs';

const PROTOCOL = 2;
const MAX_LINE = 1 << 20;

const { values: args } = parseArgs({
  options: {
    socket: { type: 'string' },
    browsers: { type: 'string' },
    version: { type: 'string' },
  },
});
for (const name of ['socket', 'browsers', 'version']) {
  if (!args[name]) {
    process.stderr.write(`browser host: --${name} is required\n`);
    process.exit(2);
  }
}

const browser = new Browser({ browsers: args.browsers, node: process.execPath });
const output = new Output(path.join(path.dirname(args.socket), 'out'));
const tabs = new Tabs({ browser, output });
browser.disconnected = () => tabs.forget();

const methods = {
  ...Object.fromEntries(['open', 'navigate', 'snapshot', 'screenshot', 'act', 'wait', 'dialog', 'resize', 'list', 'close', 'closeOwner']
    .map(name => [`tabs.${name}`, params => tabs[name](params)])),
  hello: () => ({ protocol: PROTOCOL, hostVersion: args.version, pid: process.pid }),
  status: () => browser.status(),
  launch: async () => {
    await browser.launch();
    return browser.status();
  },
  close: async () => {
    await browser.close();
    return browser.status();
  },
};

async function answer(socket, line) {
  let request;
  try {
    request = JSON.parse(line);
  } catch {
    socket.write(JSON.stringify({ id: null, error: { code: 'bad_request', message: 'the request is not JSON' } }) + '\n');
    return;
  }
  if (!authorized(request.token)) {
    socket.write(JSON.stringify({ id: request.id, error: { code: 'unauthorized', message: 'only the daemon that started this host may call it' } }) + '\n');
    return;
  }
  const method = methods[request.method];
  if (!method) {
    socket.write(JSON.stringify({ id: request.id, error: { code: 'unknown_method', message: `no method ${request.method}` } }) + '\n');
    return;
  }
  try {
    const result = await method(request.params ?? {});
    socket.write(JSON.stringify({ id: request.id, result }) + '\n');
  } catch (error) {
    socket.write(JSON.stringify({ id: request.id, error: { code: error.code ?? 'failed', message: error.message } }) + '\n');
  }
}

const server = net.createServer(socket => {
  socket.on('error', () => {});
  const lines = readline.createInterface({ input: socket, crlfDelay: Infinity });
  lines.on('line', line => {
    if (line.length > MAX_LINE) {
      socket.destroy();
      return;
    }
    answer(socket, line);
  });
});

let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  server.close();
  await browser.close();
  fs.rmSync(args.socket, { force: true });
  process.exit(0);
}

let secret = null;
function authorized(token) {
  if (!secret || typeof token !== 'string') return false;
  const given = Buffer.from(token);
  return given.length === secret.length && crypto.timingSafeEqual(given, secret);
}

readline.createInterface({ input: process.stdin }).once('line', line => {
  secret = Buffer.from(line.trim());
  if (secret.length < 32) {
    process.stderr.write('browser host: the secret on stdin is missing or too short\n');
    process.exit(2);
  }
  output.clear();
  fs.rmSync(path.join(path.dirname(args.socket), 'shots'), { recursive: true, force: true });
  fs.rmSync(args.socket, { force: true });
  server.listen(args.socket, () => fs.chmodSync(args.socket, 0o600));
});
process.stdin.on('end', stop);
process.stdin.on('close', stop);
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
