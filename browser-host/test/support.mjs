import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';

const HOST = new URL('../host.mjs', import.meta.url).pathname;

export function startHost(t, browsers) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bh-'));
  const socket = path.join(dir, 'host.sock');
  const child = spawn(process.execPath, [HOST, '--socket', socket, '--browsers', browsers ?? path.join(dir, 'browsers'), '--version', 'test'], {
    stdio: ['pipe', 'inherit', 'inherit'],
    env: { ...process.env, TMPDIR: dir },
  });
  const exited = new Promise(resolve => child.on('exit', resolve));
  t.after(async () => {
    child.stdin.end();
    await exited;
    for (const pid of processesUnder(dir, 'chrome-headless-shell')) {
      try {
        process.kill(pid, 'SIGKILL');
      } catch (error) {
        if (error.code !== 'ESRCH') throw error;
      }
    }
    fs.rmSync(dir, { recursive: true, force: true });
  });
  return { child, socket, exited, dir };
}

export async function call(socket, method, params = {}) {
  for (let attempt = 0; ; attempt++) {
    try {
      return await once(socket, method, params);
    } catch (error) {
      if (attempt > 100 || !['ENOENT', 'ECONNREFUSED'].includes(error.code)) throw error;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
  }
}

function once(socket, method, params) {
  return new Promise((resolve, reject) => {
    const connection = net.connect(socket);
    connection.on('error', reject);
    connection.on('connect', () => {
      readline.createInterface({ input: connection }).once('line', line => {
        connection.destroy();
        resolve(JSON.parse(line));
      });
      connection.write(JSON.stringify({ id: 1, method, params }) + '\n');
    });
  });
}

export function listeningPids() {
  if (process.platform === 'linux') {
    const out = execFileSync('ss', ['-ltnpH'], { encoding: 'utf8' });
    return [...out.matchAll(/pid=(\d+)/g)].map(match => Number(match[1]));
  }
  try {
    const out = execFileSync('lsof', ['-nP', '-iTCP', '-sTCP:LISTEN', '-Fp'], { encoding: 'utf8' });
    return [...out.matchAll(/^p(\d+)$/gm)].map(match => Number(match[1]));
  } catch (error) {
    if (error.status === 1) return [];
    throw error;
  }
}

export function processesUnder(dir, executable) {
  const out = execFileSync('ps', ['-eo', 'pid=,args='], { encoding: 'utf8' });
  return out.split('\n')
    .map(line => line.trim().split(/\s+/))
    .filter(([pid, command, ...args]) => args.some(arg => arg.includes(dir + path.sep))
      && (!executable || path.basename(command ?? '') === executable))
    .map(([pid]) => Number(pid))
    .filter(pid => pid !== process.pid);
}

export function descendsFrom(pid, ancestor) {
  for (let current = pid; current > 1; ) {
    if (current === ancestor) return true;
    const parent = Number(execFileSync('ps', ['-o', 'ppid=', '-p', String(current)], { encoding: 'utf8' }).trim());
    if (!parent || parent === current) return false;
    current = parent;
  }
  return false;
}
