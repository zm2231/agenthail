import assert from 'node:assert/strict';
import http from 'node:http';
import test from 'node:test';
import { clip } from '../tabs.mjs';
import { call, startHost } from './support.mjs';

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
