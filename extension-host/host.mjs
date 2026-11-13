// Protocol: docs/maintainers/extensions.md. The first stdin line is the daemon's secret; exits when stdin closes.
import crypto from 'node:crypto';
import fs from 'node:fs';
import net from 'node:net';
import readline from 'node:readline';
import { parseArgs } from 'node:util';
import { Extensions } from './extensions.mjs';

const PROTOCOL = 1;
const MAX_LINE = 1 << 20;

const { values: args } = parseArgs({
  options: {
    socket: { type: 'string' },
    version: { type: 'string' },
    store: { type: 'string' },
  },
});
for (const name of ['socket', 'version', 'store']) {
  if (!args[name]) {
    process.stderr.write(`extension host: --${name} is required\n`);
    process.exit(2);
  }
}

const extensions = new Extensions({ store: args.store });

const methods = {
  hello: () => ({ protocol: PROTOCOL, hostVersion: args.version, pid: process.pid }),
  status: () => extensions.status(),
  load: params => extensions.load(params),
  action: params => extensions.action(params),
  job: params => extensions.job(params),
  sources: params => extensions.sources(params),
  tool: params => extensions.tool(params),
  events: params => extensions.deliver(params),
};

async function answer(socket, line) {
  let request;
  try {
    request = JSON.parse(line);
  } catch {
    socket.end(JSON.stringify({ id: null, error: { code: 'bad_request', message: 'the request is not JSON' } }) + '\n');
    return;
  }
  if (!authorized(request.token)) {
    socket.end(JSON.stringify({ id: request.id, error: { code: 'unauthorized', message: 'only the daemon that started this host may call it' } }) + '\n');
    return;
  }
  const method = methods[request.method];
  if (!method) {
    socket.end(JSON.stringify({ id: request.id, error: { code: 'unknown_method', message: `no method ${request.method}` } }) + '\n');
    return;
  }
  try {
    const result = await method(request.params ?? {});
    socket.end(JSON.stringify({ id: request.id, result: result ?? {} }) + '\n');
  } catch (error) {
    socket.end(JSON.stringify({ id: request.id, error: { code: error.code ?? 'failed', message: String(error.message ?? error) } }) + '\n');
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
  await extensions.close();
  fs.rmSync(args.socket, { force: true });
  process.exit(0);
}

let secret = null;
function authorized(token) {
  if (!secret || typeof token !== 'string') return false;
  const given = Buffer.from(token);
  return given.length === secret.length && crypto.timingSafeEqual(given, secret);
}

process.on('unhandledRejection', error => {
  process.stderr.write(`extension host: unhandled rejection: ${error?.stack ?? error}\n`);
});

readline.createInterface({ input: process.stdin }).once('line', line => {
  secret = Buffer.from(line.trim());
  if (secret.length < 32) {
    process.stderr.write('extension host: the secret on stdin is missing or too short\n');
    process.exit(2);
  }
  fs.rmSync(args.socket, { force: true });
  server.listen(args.socket, () => fs.chmodSync(args.socket, 0o600));
});
process.stdin.on('end', stop);
process.stdin.on('close', stop);
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
