import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import readline from 'node:readline';
import test from 'node:test';
import { missingLibraries } from '../browser.mjs';

const HOST = new URL('../host.mjs', import.meta.url).pathname;

function startHost(t, browsers) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'bh-'));
  const socket = path.join(dir, 'host.sock');
  const child = spawn(process.execPath, [HOST, '--socket', socket, '--browsers', browsers ?? path.join(dir, 'browsers'), '--version', 'test'], { stdio: ['pipe', 'inherit', 'inherit'] });
  const exited = new Promise(resolve => child.on('exit', resolve));
  t.after(async () => {
    child.stdin.end();
    await exited;
    fs.rmSync(dir, { recursive: true, force: true });
  });
  return { child, socket, exited, dir };
}

async function call(socket, method, params = {}) {
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

function listeningPids() {
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

function descendsFrom(pid, ancestor) {
  for (let current = pid; current > 1; ) {
    if (current === ancestor) return true;
    const parent = Number(execFileSync('ps', ['-o', 'ppid=', '-p', String(current)], { encoding: 'utf8' }).trim());
    if (!parent || parent === current) return false;
    current = parent;
  }
  return false;
}

test('ldd lines that are not found name the missing libraries once each', () => {
  const ldd = [
    '\tlinux-vdso.so.1 (0x00007ffd)',
    '\tlibnss3.so => not found',
    '\tlibatk-1.0.so.0 => /lib/x86_64-linux-gnu/libatk-1.0.so.0 (0x00007f)',
    '\tlibgbm.so.1 => not found',
    '\tlibnss3.so => not found',
  ].join('\n');
  assert.deepEqual(missingLibraries(ldd), ['libgbm.so.1', 'libnss3.so']);
});

test('a host with no Chromium yet answers hello and reports it absent without downloading', async t => {
  const host = startHost(t);
  const hello = await call(host.socket, 'hello');
  assert.equal(hello.result.protocol, 1);
  assert.equal(hello.result.hostVersion, 'test');
  assert.equal(hello.result.pid, host.child.pid);
  const status = await call(host.socket, 'status');
  assert.equal(status.result.state, 'absent');
  assert.equal(status.result.running, false);
  assert.match(status.result.version, /^\d+\./);
  assert.equal(fs.existsSync(path.join(host.dir, 'browsers')), false);
  const unknown = await call(host.socket, 'navigate');
  assert.equal(unknown.error.code, 'unknown_method');
});

test('the host exits and removes its socket when its owner goes away', async t => {
  const host = startHost(t);
  await call(host.socket, 'hello');
  host.child.stdin.end();
  assert.equal(await host.exited, 0);
  assert.equal(fs.existsSync(host.socket), false);
});

test('launch downloads the pinned Chromium on first use and runs it without a debugging port', { timeout: 600_000 }, async t => {
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const launched = await call(host.socket, 'launch');
  assert.equal(launched.error, undefined, launched.error?.message);
  assert.equal(launched.result.state, 'ready');
  assert.equal(launched.result.running, true);
  const ports = listeningPids().filter(pid => descendsFrom(pid, host.child.pid));
  assert.deepEqual(ports, []);
  const closed = await call(host.socket, 'close');
  assert.equal(closed.result.running, false);
});
