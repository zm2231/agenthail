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
    if (request.url === '/lazy') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(`<title>Lazy</title><style>html { scroll-behavior: smooth } p { height: 60px }</style><main id="rows"></main><script>
        let next = 0;
        const more = () => { for (let i = 0; i < 20; i++) rows.append(Object.assign(document.createElement('p'), { textContent: 'Row ' + next++ })); };
        more();
        addEventListener('scroll', () => { if (next < 100 && innerHeight + scrollY >= document.body.scrollHeight - 10) setTimeout(more, 100); });
      </script>`);
      return;
    }
    if (request.url === '/app') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      const items = Array.from({ length: 80 }, (_, i) => `<p>Item ${i} <a href="/item/${i}">open ${i}</a></p>`).join('');
      response.end(`<title>App</title><style>html, body { height: 100%; margin: 0; overflow: hidden } #pane { height: 100%; overflow: auto }</style><div id="pane">${items}<a href="/item/0">open 0</a></div>`);
      return;
    }
    if (request.url === '/modal') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(`<title>Modal</title><p id="out">none</p><div role="dialog" aria-modal="true" aria-label="Cookies"><p>We use cookies</p>
        <button onclick="out.textContent = 'accepted'; this.parentElement.remove()">Accept all</button>
        <button aria-label="Close" onclick="out.textContent = 'closed'; this.parentElement.remove()">×</button></div>
        <div role="dialog" aria-modal="true" aria-label="Survey" hidden><button>Take it</button></div>`);
      return;
    }
    if (request.url === '/stacked') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end(`<title>Stacked</title><div role="dialog" aria-label="Plan"><p>Choose a plan</p><button>Basic</button></div>
        <div role="dialog" aria-modal="true" aria-label="News" id="news"><p>Subscribe?</p></div>
        <script>addEventListener('keydown', e => { if (e.key === 'Escape') news.remove(); });</script>`);
      return;
    }
    if (request.url === '/stuck') {
      response.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      response.end('<title>Stuck</title><div role="dialog" aria-label="Plan"><p>Choose a plan</p><button>Basic</button><button>Pro</button></div>');
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

test('text scrolls a lazy page to its end first and returns all of its text, cut to fit when long', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/lazy` });
  const first = await ok(host.socket, 'tabs.text', { limit: LIMIT, owner, tab });
  assert.ok(!first.text.includes('Row 99'), 'the lazy rows were there before scrolling');
  const loaded = await ok(host.socket, 'tabs.text', { limit: LIMIT, owner, tab, scroll: true });
  assert.equal(loaded.loaded.complete, true);
  assert.equal(loaded.truncated, false);
  assert.match(loaded.text, /Row 99/);
  assert.equal(fs.readFileSync(loaded.file, 'utf8'), loaded.text);

  const long = await ok(host.socket, 'tabs.open', { owner, url: `${url}/long` });
  const cut = await ok(host.socket, 'tabs.text', { limit: LIMIT, owner, tab: long.tab });
  assert.equal(cut.truncated, true);
  assert.ok(resultBytes(cut) <= LIMIT, `the result encodes to ${resultBytes(cut)} bytes`);
  assert.ok(cut.full.includes(cut.file), cut.full);
  assert.match(fs.readFileSync(cut.file, 'utf8'), /Buy 599/);
  const tree = fs.readFileSync((await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab: long.tab })).file, 'utf8');
  const one = await ok(host.socket, 'tabs.text', { limit: LIMIT, owner, tab: long.tab, ref: ref(tree, 'button "Buy 7"') });
  assert.equal(one.text, 'Buy 7');
});

test('scroll moves the page, or the container that scrolls when the page does not, and says whether it moved', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const page = await ok(host.socket, 'tabs.open', { owner, url: `${url}/lazy` });
  const down = await ok(host.socket, 'tabs.scroll', { owner, tab: page.tab, by: 300 });
  assert.deepEqual([down.scroller, down.moved, down.scrollTop], ['page', true, 300]);
  await ok(host.socket, 'tabs.scroll', { owner, tab: page.tab, to: 'top' });
  const again = await ok(host.socket, 'tabs.scroll', { owner, tab: page.tab, to: 'top' });
  assert.equal(again.moved, false);

  const app = await ok(host.socket, 'tabs.open', { owner, url: `${url}/app` });
  const bottom = await ok(host.socket, 'tabs.scroll', { owner, tab: app.tab, to: 'bottom' });
  assert.deepEqual([bottom.scroller, bottom.moved, bottom.atEnd], ['element', true, true]);
  await ok(host.socket, 'tabs.scroll', { owner, tab: app.tab, to: 'top' });
  const { snapshot } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab: app.tab, interactive: true });
  const shown = await ok(host.socket, 'tabs.scroll', { owner, tab: app.tab, ref: ref(snapshot, 'link "open 79"') });
  assert.deepEqual([shown.moved, shown.inView], [true, true]);
  for (const params of [{}, { by: 0 }, { to: 'middle' }, { to: 'top', by: 10 }]) {
    assert.equal((await call(host.socket, 'tabs.scroll', { owner, tab: app.tab, ...params })).error.code, 'invalid', JSON.stringify(params));
  }
  assert.equal((await call(host.socket, 'tabs.act', { owner, tab: app.tab, kind: 'scroll' })).error.code, 'invalid');
});

test('links lists each link once with its full URL, for the page or one part of it', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/app` });
  const all = await ok(host.socket, 'tabs.links', { limit: LIMIT, owner, tab });
  assert.equal(all.lines, 80);
  assert.equal(all.links.split('\n')[0], `[open 0](${url}/item/0)`);
  const { snapshot } = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab });
  const part = await ok(host.socket, 'tabs.links', { limit: LIMIT, owner, tab, ref: ref(snapshot, 'link "open 5"') });
  assert.equal(part.links, `[open 5](${url}/item/5)`);
});

test('a snapshot narrows to the interactive elements, a ref or a selector, or a depth', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/long` });
  const interactive = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab, interactive: true });
  const lines = fs.readFileSync(interactive.file, 'utf8').split('\n');
  assert.equal(lines.length, 1200);
  assert.ok(lines.every(line => /^- (link|button) .*\[ref=e\d+\]/.test(line)), lines.find(line => !/^- (link|button)/.test(line)));
  await ok(host.socket, 'tabs.act', { owner, tab, kind: 'click', ref: ref(lines.join('\n'), 'button "Buy 3"') });
  await ok(host.socket, 'tabs.wait', { owner, tab, text: 'bought 3', timeoutMs: 5000 });

  const scoped = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab, selector: 'li:nth-child(2)' });
  assert.equal(scoped.truncated, false);
  assert.match(scoped.snapshot, /Buy 1/);
  assert.doesNotMatch(scoped.snapshot, /Buy 2/);
  const shallow = await ok(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab, depth: 1 });
  assert.ok(shallow.bytes < interactive.bytes / 10, `depth 1 is ${shallow.bytes} bytes`);
  for (const params of [{ ref: 'e1', selector: 'li' }, { depth: 0 }, { depth: -1 }, { depth: 1.5 }]) {
    assert.equal((await call(host.socket, 'tabs.snapshot', { limit: LIMIT, owner, tab, ...params })).error.code, 'invalid', JSON.stringify(params));
  }
});

test('dismiss closes a modal with its Close button, never Accept, and says when it could not', { timeout: 600_000 }, async t => {
  const url = await serve(t);
  const host = startHost(t, process.env.AGENTHAIL_BROWSER_TEST_BROWSERS);
  const owner = 'session-a';
  const { tab } = await ok(host.socket, 'tabs.open', { owner, url: `${url}/modal` });
  const closed = await ok(host.socket, 'tabs.dismiss', { owner, tab });
  assert.deepEqual([closed.dismissed, closed.by], [true, 'button "Close"']);
  await ok(host.socket, 'tabs.wait', { owner, tab, text: 'closed', timeoutMs: 5000 });
  const none = await ok(host.socket, 'tabs.dismiss', { owner, tab });
  assert.equal(none.dismissed, false);

  const stuck = await ok(host.socket, 'tabs.open', { owner, url: `${url}/stuck` });
  const refused = await ok(host.socket, 'tabs.dismiss', { owner, tab: stuck.tab });
  assert.equal(refused.dismissed, false);
  assert.match(refused.detail, /no Close or Dismiss button/);

  const stacked = await ok(host.socket, 'tabs.open', { owner, url: `${url}/stacked` });
  const top = await ok(host.socket, 'tabs.dismiss', { owner, tab: stacked.tab });
  assert.deepEqual([top.dismissed, top.by, top.open], [true, 'Escape', 1]);
});
