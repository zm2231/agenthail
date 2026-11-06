import '../../scripts/test-home.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { missingLibraries } from '../browser.mjs';
import { call, descendsFrom, listeningPids, processesUnder, startHost } from './support.mjs';

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
  assert.equal(hello.result.protocol, 2);
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
  const chromium = processesUnder(host.dir).filter(pid => pid !== host.child.pid);
  assert.notDeepEqual(chromium, []);
  assert.ok(chromium.every(pid => descendsFrom(pid, host.child.pid)));
  const ports = listeningPids().filter(pid => descendsFrom(pid, host.child.pid));
  assert.deepEqual(ports, []);
  const closed = await call(host.socket, 'close');
  assert.equal(closed.result.running, false);
});

test('Chromium exits when its host is killed', { timeout: 600_000 }, async t => {
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const launched = await call(host.socket, 'launch');
  assert.equal(launched.error, undefined, launched.error?.message);
  assert.notDeepEqual(processesUnder(host.dir).filter(pid => pid !== host.child.pid), []);
  host.child.kill('SIGKILL');
  await host.exited;
  for (let attempt = 0; processesUnder(host.dir).length > 0; attempt++) {
    assert.ok(attempt < 100, `Chromium outlived its host: ${processesUnder(host.dir)}`);
    await new Promise(resolve => setTimeout(resolve, 100));
  }
});
