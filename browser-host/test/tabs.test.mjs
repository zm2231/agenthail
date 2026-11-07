import '../../scripts/test-home.mjs';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import http from 'node:http';
import test from 'node:test';
import { clip } from '../tabs.mjs';
import { call, callWith, processesUnder, startHost } from './support.mjs';

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

test('a snapshot over 20 KiB is cut at a line and says so', () => {
  const long = Array.from({ length: 3000 }, (_, i) => `- line ${i}`).join('\n');
  const { text, truncated } = clip(long);
  assert.equal(truncated, true);
  assert.ok(Buffer.byteLength(text) <= 20 << 10);
  assert.ok(text.endsWith('\n'));
  assert.deepEqual(clip('- short'), { text: '- short', truncated: false });
});

test('an agent opens a page, acts on snapshot refs, waits for text and answers a dialog', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const opened = await ok(host.socket, 'tabs.open', { owner, url });
  assert.equal(opened.title, 'Home');
  const tab = opened.tab;
  let { snapshot } = await ok(host.socket, 'tabs.snapshot', { owner, tab });
  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'type', ref: ref(snapshot, 'textbox "Name"'), text: 'Ada' });
  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(snapshot, 'button "Go"') });
  ({ snapshot } = await ok(host.socket, 'tabs.snapshot', { owner, tab }));
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
    for (const [method, params] of [['hello', {}], ['tabs.list', { owner }], ['tabs.snapshot', { owner, tab }], ['tabs.close', { owner, tab }], ['tabs.closeOwner', { owner }]]) {
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
  const blocked = await ok(host.socket, 'tabs.snapshot', { owner, tab });
  assert.equal(blocked.snapshot, undefined);
  assert.ok(Date.now() - started < 10_000, `took ${Date.now() - started} ms`);
  await ok(host.socket, 'tabs.dialog', { owner, tab, accept: true });
  const { snapshot } = await ok(host.socket, 'tabs.snapshot', { owner, tab });
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
  const parentOf = pid => Number(execFileSync('ps', ['-o', 'ppid=', '-p', String(pid)], { encoding: 'utf8' }).trim());
  const [chromium] = processesUnder(host.dir, 'chrome-headless-shell').filter(pid => parentOf(pid) === host.child.pid);
  assert.ok(chromium, 'no Chromium under the host');
  process.kill(chromium, 'SIGKILL');
  for (let attempt = 0; (await ok(host.socket, 'tabs.list', { owner })).tabs.length > 0; attempt++) {
    assert.ok(attempt < 100, 'the tab outlived its browser');
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  assert.equal((await call(host.socket, 'tabs.snapshot', { owner, tab })).error.code, 'not_found');
  const reopened = await ok(host.socket, 'tabs.open', { owner, url });
  assert.notEqual(reopened.tab, tab);
  assert.equal(reopened.title, 'Home');
});

test('each owner has its own cookies and cannot reach another owner\'s tabs', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const first = await ok(host.socket, 'tabs.open', { owner: 'session-a', url: `${url}/login` });
  const mine = await ok(host.socket, 'tabs.open', { owner: 'session-a', url: `${url}/whoami` });
  assert.match((await ok(host.socket, 'tabs.snapshot', { owner: 'session-a', tab: mine.tab })).snapshot, /cookie: session=secret/);
  const other = await ok(host.socket, 'tabs.open', { owner: 'session-b', url: `${url}/whoami` });
  assert.match((await ok(host.socket, 'tabs.snapshot', { owner: 'session-b', tab: other.tab })).snapshot, /cookie: none/);

  assert.equal((await call(host.socket, 'tabs.snapshot', { owner: 'session-b', tab: first.tab })).error.code, 'not_found');
  assert.equal((await call(host.socket, 'tabs.close', { owner: 'session-b', tab: first.tab })).error.code, 'not_found');
  assert.deepEqual((await ok(host.socket, 'tabs.list', { owner: 'session-b' })).tabs.map(entry => entry.tab), [other.tab]);

  const closed = await ok(host.socket, 'tabs.closeOwner', { owner: 'session-a' });
  assert.deepEqual(closed.closed.sort(), [first.tab, mine.tab].sort());
  assert.deepEqual((await ok(host.socket, 'tabs.list', { owner: 'session-a' })).tabs, []);
  assert.equal((await ok(host.socket, 'tabs.list', { owner: 'session-b' })).tabs.length, 1);
});
