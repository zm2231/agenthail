// Protocol: docs/maintainers/browser-host.md. Exits when stdin (the daemon) closes.
import fs from 'node:fs';
import net from 'node:net';
import readline from 'node:readline';
import { parseArgs } from 'node:util';
import { Browser } from './browser.mjs';

const PROTOCOL = 1;
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

const methods = {
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

fs.rmSync(args.socket, { force: true });
server.listen(args.socket, () => fs.chmodSync(args.socket, 0o600));
process.stdin.on('end', stop);
process.stdin.on('close', stop);
process.stdin.resume();
process.on('SIGTERM', stop);
process.on('SIGINT', stop);
