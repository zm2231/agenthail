import '../../scripts/test-home.mjs';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import test from 'node:test';
import os from 'node:os';
import { encodedBytes, excerpt, Output } from '../output.mjs';
import { Tabs } from '../tabs.mjs';
import { call, callWith, processesUnder, startHost } from './support.mjs';

const LIMIT = 16 << 10;

function resultBytes(value) {
  const json = JSON.stringify(value);
  return Buffer.byteLength(json) + 5 * (json.match(/[<>&\u2028\u2029]/g)?.length ?? 0);
}

const PAGE = `<!doctype html><title>Home</title>
<button onclick="out.textContent = 'clicked ' + nameField.value">Go</button>
<input id="nameField" aria-label="Name">
<button onclick="setTimeout(() => document.body.append(Object.assign(document.createElement('p'), { textContent: 'arrived later' })), 300)">Later</button>
<button onclick="out.textContent = confirm('Sure?') ? 'confirmed' : 'declined'">Ask</button>
<p id="out">idle</p>`;

function serve(t) {
  const server = http.createServer((request, response) => {
    if (request.url === '/login') {
      response.writeHead(200, { 'set-cookie': 'session=secret; Path=/', 'content-type': 'text/html' });
      response.end('<title>Login</title><p>signed in</p>');
      return;
    }
    if (request.url === '/alerts') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end('<title>Alerts</title><script>alert("loading")</script><p>loaded</p><button onclick="setTimeout(() => alert(\'later\'), 200)">Soon</button>');
      return;
    }
    if (request.url === '/long') {
      response.writeHead(200, { 'content-type': 'text/html' });
      const rows = Array.from({ length: 600 }, (_, i) => `<li><a href="#${i}">"quoted" <item> & ${i}</a> <button onclick="out.textContent='bought ${i}'">Buy ${i}</button></li>`).join('');
      response.end(`<title>Long</title><p id="out">none</p><ul>${rows}</ul>`);
      return;
    }
    if (request.url.startsWith('/titled')) {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end(`<title>${'<&>'.repeat(7000)}</title><p>body</p>`);
      return;
    }
    if (request.url === '/whoami') {
      response.writeHead(200, { 'content-type': 'text/html' });
      response.end(`<title>Who</title><p>cookie: ${request.headers.cookie ?? 'none'}</p>`);
      return;
    }
    response.writeHead(200, { 'content-type': 'text/html' });
    response.end(PAGE);
  });
  return new Promise(resolve => {
    server.listen(0, '127.0.0.1', () => {
      t.after(() => server.close());
      resolve(`http://127.0.0.1:${server.address().port}`);
    });
  });
}

function ref(snapshot, line) {
  const found = snapshot.split('\n').find(text => text.includes(line));
  assert.ok(found, `no ${line} in\n${snapshot}`);
  return found.match(/\[ref=(\w+)\]/)[1];
}

async function ok(socket, method, params) {
  const response = await call(socket, method, params);
  assert.equal(response.error, undefined, `${method}: ${response.error?.message}`);
  return response.result;
}

test('an excerpt is cut at a line so its encoding, with the daemon\'s escapes, fits the budget', () => {
  const long = Array.from({ length: 3000 }, (_, i) => `- link "<a> & <b> ${i}"`).join('\n');
  const { text, truncated } = excerpt(long, 4096);
  assert.equal(truncated, true);
  assert.ok(encodedBytes(text) <= 4096, `encoded ${encodedBytes(text)}`);
  assert.ok(encodedBytes(text) > 3800, `encoded ${encodedBytes(text)}, the budget is not used`);
  assert.ok(text.endsWith('\n'));
  assert.deepEqual(excerpt('- short', 4096), { text: '- short', truncated: false });
});

test('an agent opens a page, acts on snapshot refs, waits for text and answers a dialog', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const opened = await ok(host.socket, 'tabs.open', { owner, url });
  assert.equal(opened.title, 'Home');
  const tab = opened.tab;
  let { snapshot } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'type', ref: ref(snapshot, 'textbox "Name"'), text: 'Ada' });
  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(snapshot, 'button "Go"') });
  ({ snapshot } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab }));
  assert.match(snapshot, /clicked Ada/);

  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(snapshot, 'button "Later"') });
  await ok(host.socket, 'tabs.wait', { owner, tab, text: 'arrived later', timeoutMs: 5000 });

  const asked = await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(snapshot, 'button "Ask"') });
  assert.deepEqual(asked.dialog, { type: 'confirm', message: 'Sure?' });
  assert.equal((await call(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: 'e1' })).error.code, 'dialog_open');
  await ok(host.socket, 'tabs.dialog', { owner, tab, accept: true });
  await ok(host.socket, 'tabs.wait', { owner, tab, text: 'confirmed', timeoutMs: 5000 });

  const listed = await ok(host.socket, 'tabs.list', { owner });
  assert.deepEqual(listed.tabs.map(entry => entry.tab), [tab]);
  await ok(host.socket, 'tabs.close', { owner, tab });
  assert.deepEqual((await ok(host.socket, 'tabs.list', { owner })).tabs, []);
});

test('a client without the daemon\'s secret reaches no tab, even naming its owner', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url });
  for (const token of [undefined, '', 'guessed-secret-of-the-same-length!!!']) {
    for (const [method, params] of [['hello', {}], ['tabs.list', { owner }], ['tabs.snapshot', { limit: LIMIT, owner, tab }], ['tabs.close', { owner, tab }], ['tabs.closeOwner', { owner }]]) {
      const response = await callWith(token, host.socket, method, params);
      assert.equal(response.error?.code, 'unauthorized', `${method} with token ${JSON.stringify(token)}: ${JSON.stringify(response)}`);
    }
  }
  assert.deepEqual((await ok(host.socket, 'tabs.list', { owner })).tabs.map(entry => entry.tab), [tab]);
});

test('a dialog opened while a page loads or during a wait is returned at once', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner });
  const started = Date.now();
  const navigated = await ok(host.socket, 'tabs.navigate', { owner, tab, url: `${url}/alerts` });
  assert.deepEqual(navigated.dialog, { type: 'alert', message: 'loading' });
  const blocked = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  assert.equal(blocked.snapshot, undefined);
  assert.ok(Date.now() - started < 10_000, `took ${Date.now() - started} ms`);
  await ok(host.socket, 'tabs.dialog', { owner, tab, accept: true });
  const { snapshot } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  assert.match(snapshot, /loaded/);

  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(snapshot, 'button "Soon"') });
  const waited = Date.now();
  const interrupted = await ok(host.socket, 'tabs.wait', { owner, tab, text: 'never appears', timeoutMs: 20_000 });
  assert.deepEqual(interrupted.dialog, { type: 'alert', message: 'later' });
  assert.ok(Date.now() - waited < 10_000, `wait took ${Date.now() - waited} ms`);
});

test('when Chromium crashes under a running host its tabs are gone and open starts a new browser', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url });
  const { file } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  const parentOf = pid => Number(execFileSync('ps', ['-o', 'ppid=', '-p', String(pid)], { encoding: 'utf8' }).trim());
  const [chromium] = processesUnder(host.dir, 'chrome-headless-shell').filter(pid => parentOf(pid) === host.child.pid);
  assert.ok(chromium, 'no Chromium under the host');
  process.kill(chromium, 'SIGKILL');
  for (let attempt = 0; (await ok(host.socket, 'tabs.list', { owner })).tabs.length > 0; attempt++) {
    assert.ok(attempt < 100, 'the tab outlived its browser');
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.equal((await call(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab })).error.code, 'not_found');
  assert.equal(fs.existsSync(file), false, 'a snapshot outlived the browser that took it');
  const reopened = await ok(host.socket, 'tabs.open', { owner, url });
  assert.notEqual(reopened.tab, tab);
  assert.equal(reopened.title, 'Home');
});

test('each owner has its own cookies and cannot reach another owner\'s tabs', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const first = await ok(host.socket, 'tabs.open', { owner: 'session-a', url: `${url}/login` });
  const mine = await ok(host.socket, 'tabs.open', { owner: 'session-a', url: `${url}/whoami` });
  assert.match((await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner: 'session-a', tab: mine.tab })).snapshot, /cookie: session=secret/);
  const other = await ok(host.socket, 'tabs.open', { owner: 'session-b', url: `${url}/whoami` });
  assert.match((await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner: 'session-b', tab: other.tab })).snapshot, /cookie: none/);

  assert.equal((await call(host.socket, 'tabs.snapshot', { limit: LIMIT, owner: 'session-b', tab: first.tab })).error.code, 'not_found');
  assert.equal((await call(host.socket, 'tabs.close', { owner: 'session-b', tab: first.tab })).error.code, 'not_found');
  assert.deepEqual((await ok(host.socket, 'tabs.list', { owner: 'session-b' })).tabs.map(entry => entry.tab), [other.tab]);

  const closed = await ok(host.socket, 'tabs.closeOwner', { owner: 'session-a' });
  assert.deepEqual(closed.closed.sort(), [first.tab, mine.tab].sort());
  assert.deepEqual((await ok(host.socket, 'tabs.list', { owner: 'session-a' })).tabs, []);
  assert.equal((await ok(host.socket, 'tabs.list', { owner: 'session-b' })).tabs.length, 1);
});

test('a snapshot too long to return whole fits the limit, names the file that holds all of it, and its refs work', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/long` });
  const taken = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  assert.equal(taken.truncated, true);
  assert.ok(resultBytes(taken) <= LIMIT, `the result encodes to ${resultBytes(taken)} bytes`);
  assert.ok(taken.full.includes(taken.file) && /refs are valid/.test(taken.full), taken.full);
  const whole = fs.readFileSync(taken.file, 'utf8');
  assert.equal(Buffer.byteLength(whole), taken.bytes);
  assert.ok(taken.bytes > LIMIT, `the tree is only ${taken.bytes} bytes`);
  assert.ok(whole.startsWith(taken.snapshot));
  assert.ok(!taken.snapshot.includes('button "Buy 599"'));
  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(whole, 'button "Buy 599"') });
  await ok(host.socket, 'tabs.wait', { owner, tab, text: 'bought 599', timeoutMs: 5000 });
  assert.equal((fs.statSync(taken.file).mode & 0o777), 0o600);
  assert.equal((fs.statSync(path.dirname(taken.file)).mode & 0o777), 0o700);

  const small = await ok(host.socket, 'tabs.open', { owner, url });
  const whole2 = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab: small.tab });
  assert.equal(whole2.truncated, false);
  assert.equal(whole2.full, undefined);
  assert.equal(fs.readFileSync(whole2.file, 'utf8'), whole2.snapshot);
});

test('a screenshot saves the view, the whole page or one element, for its owner only', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/long` });
  const view = await ok(host.socket, 'tabs.screenshot', { owner, tab });
  assert.deepEqual([view.width, view.height], [1280, 720]);
  const page = await ok(host.socket, 'tabs.screenshot', { owner, tab, fullPage: true });
  assert.ok(page.height > 720 * 4, `full page height ${page.height}`);
  const { snapshot } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  const element = await ok(host.socket, 'tabs.screenshot', { owner, tab, ref: ref(snapshot, 'button "Buy 0"') });
  assert.ok(element.width < 200 && element.height < 100, `element ${element.width}x${element.height}`);
  for (const shot of [view, page, element]) {
    const png = fs.readFileSync(shot.file);
    assert.equal(png.length, shot.bytes);
    assert.equal(png.subarray(1, 4).toString(), 'PNG');
  }
  assert.equal((await call(host.socket, 'tabs.screenshot', { owner, tab, fullPage: true, ref: 'e1' })).error.code, 'invalid');
  assert.equal((await call(host.socket, 'tabs.screenshot', { owner: 'session-b', tab })).error.code, 'not_found');

  await ok(host.socket, 'tabs.closeOwner', { owner });
  for (const shot of [view, page, element]) assert.equal(fs.existsSync(shot.file), false, `${shot.file} outlived its owner`);
});

test('a page whose title and URL are each over the limit still returns a result that fits', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/titled#${'x'.repeat(20_000)}` });
  for (const [method, params] of [['tabs.snapshot', { limit: LIMIT }], ['tabs.screenshot', {}], ['tabs.list', {}]]) {
    const result = await ok(host.socket, method, { owner, tab, ...params });
    assert.ok(resultBytes(result) <= LIMIT, `${method} encodes to ${resultBytes(result)} bytes`);
  }
  const taken = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  assert.ok(taken.title.endsWith('…') && taken.url.endsWith('…'), `${taken.title.slice(-20)} ${taken.url.slice(-20)}`);
  assert.match(fs.readFileSync(taken.file, 'utf8'), /body/);
});

test('an owner keeps no more than its cap, and one result over it is refused', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'out-'));
  try {
    const output = new Output(root, 1000);
    const first = output.write('a', 't1', 'txt', 'x'.repeat(600));
    const second = output.write('a', 't1', 'txt', 'y'.repeat(600));
    assert.equal(fs.existsSync(first), false);
    assert.equal(fs.readFileSync(second, 'utf8'), 'y'.repeat(600));
    assert.throws(() => output.write('a', 't1', 'txt', 'z'.repeat(1001)), { code: 'too_large' });
    const kept = fs.readdirSync(output.dir('a')).reduce((sum, name) => sum + fs.statSync(path.join(output.dir('a'), name)).size, 0);
    assert.ok(kept <= 1000, `kept ${kept} bytes`);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('a snapshot still running when its owner is closed writes nothing', async () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'out-'));
  try {
    const output = new Output(root);
    const tabs = new Tabs({ browser: {}, output });
    let release;
    const page = { on() {}, off() {}, url: () => 'about:blank', title: async () => '', close: async () => {}, ariaSnapshot: () => new Promise(resolve => { release = resolve; }) };
    tabs.tabs.set('t1', { id: 't1', owner: 'a', page, dialog: null });
    const taking = tabs.snapshot({ owner: 'a', tab: 't1', limit: LIMIT });
    await tabs.closeOwner({ owner: 'a' });
    release('- generic [ref=e1]');
    await assert.rejects(taking, { code: 'not_found' });
    assert.equal(fs.existsSync(output.dir('a')), false);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});
