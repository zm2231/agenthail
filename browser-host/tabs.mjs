import { randomBytes } from 'node:crypto';
import { clipText, encodedBytes, excerpt } from './output.mjs';

const MIN_LIMIT = 1 << 10;
const URL_BYTES = 2 << 10;
const TITLE_BYTES = 512;
const MESSAGE_BYTES = 1 << 10;
const ACTION_TIMEOUT_MS = 10_000;
const NAVIGATE_TIMEOUT_MS = 30_000;
const MAX_WAIT_MS = 30_000;
const IDLE_MS = 5_000;
const LOAD_ALL_MS = 20_000;
const LOAD_STEP_MS = 500;
const KINDS = new Set(['click', 'type', 'select', 'hover', 'drag', 'press', 'upload']);
const INTERACTIVE = new Set(['button', 'link', 'textbox', 'searchbox', 'checkbox', 'radio', 'combobox', 'listbox', 'option', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'tab', 'switch', 'slider', 'spinbutton', 'treeitem']);
const CLOSE_NAME = /^(close|dismiss|×|✕|✖|x)$/i;

// Runs in the page. Without self it picks the page when it scrolls, or else
// the scrollable element covering most of the view, since many apps scroll a
// container rather than the document. Instant behavior keeps a CSS
// scroll-behavior: smooth from reporting the old position.
function scrollIn(node, { to, by, self }) {
  const page = document.scrollingElement || document.documentElement;
  const scrollable = el => el.scrollHeight > el.clientHeight + 1 && /(auto|scroll|overlay)/.test(getComputedStyle(el).overflowY);
  let el = node;
  if (!self) {
    el = page;
    if (page.scrollHeight <= innerHeight + 1) {
      let area = 0;
      for (const candidate of document.querySelectorAll('*')) {
        if (!scrollable(candidate)) continue;
        const r = candidate.getBoundingClientRect();
        const seen = Math.max(0, Math.min(r.right, innerWidth) - Math.max(r.left, 0)) * Math.max(0, Math.min(r.bottom, innerHeight) - Math.max(r.top, 0));
        if (seen > area) [el, area] = [candidate, seen];
      }
    }
  }
  const before = el.scrollTop;
  const top = to === 'top' ? 0 : to === 'bottom' ? el.scrollHeight : before + (by ?? 0);
  el.scrollTo({ top, behavior: 'instant' });
  const clientHeight = el === page ? innerHeight : el.clientHeight;
  return { scroller: el === page ? 'page' : 'element', moved: el.scrollTop !== before, scrollTop: Math.round(el.scrollTop), scrollHeight: el.scrollHeight, clientHeight, atEnd: el.scrollTop + clientHeight >= el.scrollHeight - 1 };
}

function linksIn(node) {
  const seen = new Set();
  const lines = [];
  const anchors = node.matches('a[href]') ? [node] : [];
  for (const a of [...anchors, ...node.querySelectorAll('a[href]')]) {
    if (!/^https?:|^mailto:|^tel:/.test(a.href)) continue;
    const text = (a.innerText || a.getAttribute('aria-label') || a.title || '').replace(/\s+/g, ' ').trim();
    const line = `[${text}](${a.href})`;
    if (seen.has(line)) continue;
    seen.add(line);
    lines.push(line);
  }
  return lines.join('\n');
}

export function interactiveLines(tree) {
  return tree.split('\n').flatMap(line => {
    const match = /^\s*- ([a-z]+)\b.*\[ref=e\d+\]/.exec(line);
    return match && INTERACTIVE.has(match[1]) ? [line.trim().replace(/:$/, '')] : [];
  }).join('\n');
}

function failure(code, message) {
  return Object.assign(new Error(message), { code });
}

export class Tabs {
  constructor({ browser, output }) {
    this.browser = browser;
    this.output = output;
    this.contexts = new Map();
    this.tabs = new Map();
  }

  async open({ owner, url }) {
    if (!owner) throw failure('invalid', 'owner is required');
    await this.browser.launch();
    const context = await this.context(owner);
    const page = await context.newPage();
    const tab = { id: `t${randomBytes(6).toString('hex')}`, owner, page, dialog: null };
    page.on('dialog', dialog => { tab.dialog = dialog; });
    page.on('close', () => this.tabs.delete(tab.id));
    this.tabs.set(tab.id, tab);
    if (url) await this.goto(tab, url);
    return this.describe(tab);
  }

  async context(owner) {
    let context = this.contexts.get(owner);
    if (!context) {
      context = await this.browser.browser.newContext({ viewport: { width: 1280, height: 720 } });
      context.on('close', () => {
        if (this.contexts.get(owner) === context) this.contexts.delete(owner);
      });
      this.contexts.set(owner, context);
    }
    return context;
  }

  tab({ owner, tab: id }) {
    const tab = this.tabs.get(id);
    if (!tab || tab.owner !== owner) throw failure('not_found', `no tab ${id}: it was closed or the browser restarted; list your tabs or open a new one`);
    return tab;
  }

  async describe(tab) {
    const out = { tab: tab.id, url: clipText(tab.page.url(), URL_BYTES), title: clipText(await tab.page.title().catch(() => ''), TITLE_BYTES) };
    if (tab.dialog) out.dialog = { type: tab.dialog.type(), message: clipText(tab.dialog.message(), MESSAGE_BYTES) };
    return out;
  }

  keep(tab, extension, data) {
    if (this.tabs.get(tab.id) !== tab) throw failure('not_found', `tab ${tab.id} was closed while this ran`);
    return this.output.write(tab.owner, tab.id, extension, data);
  }

  async guarded(tab, run) {
    if (tab.dialog) return;
    let opened;
    const dialog = new Promise(resolve => { opened = resolve; });
    tab.page.on('dialog', opened);
    const work = run();
    try {
      await Promise.race([work, dialog]);
    } finally {
      tab.page.off('dialog', opened);
      work.catch(() => {});
    }
  }

  async goto(tab, url) {
    try {
      await this.guarded(tab, () => tab.page.goto(url, { timeout: NAVIGATE_TIMEOUT_MS }));
    } catch (error) {
      throw failure('navigation_failed', error.message.split('\n')[0]);
    }
  }

  async navigate(params) {
    const tab = this.tab(params);
    await this.goto(tab, params.url);
    return this.describe(tab);
  }

  limit(params) {
    const limit = Number(params.limit);
    if (!(limit >= MIN_LIMIT)) throw failure('invalid', `limit must be at least ${MIN_LIMIT} bytes`);
    return limit;
  }

  // paged returns a page-text result whole when it fits under limit, and
  // otherwise its head with the file that keeps all of it.
  paged(tab, out, { extension, field, noun, note = '' }, body, limit) {
    const file = this.keep(tab, extension, body);
    const bytes = Buffer.byteLength(body);
    const lines = body === '' ? 0 : body.split('\n').length;
    const full = `The whole ${noun} is in ${file} (${bytes} bytes, ${lines} lines); read it there.${note}`;
    const rest = { ...out, file, bytes, lines, truncated: true, full, [field]: '' };
    const { text, truncated } = excerpt(body, limit - encodedBytes(JSON.stringify(rest)));
    if (truncated) return { ...rest, [field]: text };
    delete rest.full;
    return { ...rest, truncated: false, [field]: text };
  }

  root(tab, { ref, selector }) {
    if (ref && selector) throw failure('invalid', 'give ref or selector, not both');
    if (ref) return this.locate(tab, ref);
    if (selector) return tab.page.locator(selector);
    return null;
  }

  async snapshot(params) {
    const tab = this.tab(params);
    const limit = this.limit(params);
    const depth = params.depth ?? undefined;
    if (depth !== undefined && !(Number.isInteger(depth) && depth >= 1)) throw failure('invalid', 'depth must be a whole number of levels, at least 1');
    const root = this.root(tab, params);
    let tree;
    try {
      await this.guarded(tab, async () => {
        const options = { mode: 'ai', depth, timeout: ACTION_TIMEOUT_MS };
        tree = await (root ?? tab.page).ariaSnapshot(options);
      });
    } catch (error) {
      throw failure(error.code === 'invalid' ? 'invalid' : 'action_failed', error.message.split('\n')[0]);
    }
    const out = await this.describe(tab);
    if (tab.dialog || tree === undefined) return out;
    if (params.interactive) tree = interactiveLines(tree);
    const note = root ? ' Its refs are valid until your next snapshot; refs outside this part of the page stop working now.' : ' Its refs are valid until your next snapshot.';
    return this.paged(tab, out, { extension: 'snapshot.yml', field: 'snapshot', noun: 'snapshot', note }, tree, limit);
  }

  async idle(tab) {
    try {
      await tab.page.waitForLoadState('networkidle', { timeout: IDLE_MS });
      return true;
    } catch {
      return false;
    }
  }

  async loadAll(tab) {
    const html = tab.page.locator('html');
    const deadline = Date.now() + LOAD_ALL_MS;
    let height = -1;
    let still = 0;
    let last;
    while (Date.now() < deadline && still < 2) {
      last = await html.evaluate(scrollIn, { to: 'bottom' });
      still = last.scrollHeight === height ? still + 1 : 0;
      height = last.scrollHeight;
      await tab.page.waitForTimeout(LOAD_STEP_MS);
    }
    return { complete: still >= 2, idle: await this.idle(tab), scrollHeight: height };
  }

  async text(params) {
    const tab = this.tab(params);
    const limit = this.limit(params);
    let body;
    let loaded;
    try {
      await this.guarded(tab, async () => {
        if (params.scroll) loaded = await this.loadAll(tab);
        body = await (this.root(tab, params) ?? tab.page.locator('body')).innerText({ timeout: ACTION_TIMEOUT_MS });
      });
    } catch (error) {
      throw failure(error.code === 'invalid' ? 'invalid' : 'action_failed', error.message.split('\n')[0]);
    }
    const out = await this.describe(tab);
    if (tab.dialog || body === undefined) return out;
    if (loaded) out.loaded = loaded;
    return this.paged(tab, out, { extension: 'txt', field: 'text', noun: 'text' }, body, limit);
  }

  async links(params) {
    const tab = this.tab(params);
    const limit = this.limit(params);
    let body;
    try {
      await this.guarded(tab, async () => {
        body = await (this.root(tab, params) ?? tab.page.locator('body')).evaluate(linksIn);
      });
    } catch (error) {
      throw failure(error.code === 'invalid' ? 'invalid' : 'action_failed', error.message.split('\n')[0]);
    }
    const out = await this.describe(tab);
    if (tab.dialog || body === undefined) return out;
    return this.paged(tab, out, { extension: 'links.md', field: 'links', noun: 'link list' }, body, limit);
  }

  async scroll(params) {
    const tab = this.tab(params);
    const { to, ref } = params;
    const by = params.by ?? undefined;
    if (to !== undefined && to !== null && to !== '' && to !== 'top' && to !== 'bottom') throw failure('invalid', 'to must be top or bottom');
    const target = to || by !== undefined;
    if (to && by !== undefined) throw failure('invalid', 'give to or by, not both');
    if (by !== undefined && !(Number.isFinite(by) && by !== 0)) throw failure('invalid', 'by must be a non-zero number of CSS pixels, negative for up');
    if (!target && !ref) throw failure('invalid', 'scroll needs to (top or bottom), by (pixels, negative for up) or a ref to bring into view');
    if (tab.dialog) throw failure('dialog_open', 'a dialog is open; answer it with the dialog action first');
    let result;
    try {
      await this.guarded(tab, async () => {
        if (target) {
          result = await (ref ? this.locate(tab, ref) : tab.page.locator('html')).evaluate(scrollIn, { to: to || undefined, by, self: Boolean(ref) });
          return;
        }
        const element = this.locate(tab, ref);
        const before = await element.boundingBox({ timeout: ACTION_TIMEOUT_MS });
        await element.scrollIntoViewIfNeeded({ timeout: ACTION_TIMEOUT_MS });
        const after = await element.boundingBox({ timeout: ACTION_TIMEOUT_MS });
        const view = tab.page.viewportSize();
        result = { moved: before?.y !== after?.y, inView: Boolean(after && view && after.y < view.height && after.y + after.height > 0) };
      });
    } catch (error) {
      throw failure(error.code === 'invalid' ? 'invalid' : 'action_failed', error.message.split('\n')[0]);
    }
    const out = await this.describe(tab);
    if (tab.dialog || !result) return out;
    if (params.idle) result.idle = await this.idle(tab);
    return { ...out, ...result };
  }

  async dismiss(params) {
    const tab = this.tab(params);
    if (tab.dialog) throw failure('dialog_open', 'a browser dialog is open; answer it with the dialog action');
    const modals = tab.page.locator('[role=dialog]:visible, [role=alertdialog]:visible, [aria-modal=true]:visible, dialog[open]:visible');
    let result;
    try {
      await this.guarded(tab, async () => {
        const before = await modals.count();
        await tab.page.keyboard.press('Escape');
        await tab.page.waitForTimeout(200);
        const after = await modals.count();
        if (after < before) {
          result = { dismissed: true, by: 'Escape', open: after };
          return;
        }
        if (after === 0) {
          result = { dismissed: false, detail: 'No dialog or modal was open; Escape was pressed.' };
          return;
        }
        const close = modals.first().getByRole('button', { name: CLOSE_NAME }).first();
        if (!await close.count()) {
          result = { dismissed: false, detail: 'A dialog is still open and has no Close or Dismiss button; take a snapshot and choose its button yourself.' };
          return;
        }
        const name = (await close.getAttribute('aria-label')) || (await close.innerText()).trim();
        await close.click({ timeout: ACTION_TIMEOUT_MS });
        await tab.page.waitForTimeout(200);
        const open = await modals.count();
        result = { dismissed: open < after, by: `button "${name}"`, open };
      });
    } catch (error) {
      throw failure('action_failed', error.message.split('\n')[0]);
    }
    const out = await this.describe(tab);
    return result ? { ...out, ...result } : out;
  }

  async screenshot(params) {
    const tab = this.tab(params);
    if (params.fullPage && params.ref) throw failure('invalid', 'fullPage and ref cannot be combined: a ref captures one element');
    let image;
    try {
      await this.guarded(tab, async () => {
        image = params.ref
          ? await this.locate(tab, params.ref).screenshot({ timeout: ACTION_TIMEOUT_MS })
          : await tab.page.screenshot({ fullPage: Boolean(params.fullPage), timeout: ACTION_TIMEOUT_MS });
      });
    } catch (error) {
      throw failure(error.code === 'invalid' ? 'invalid' : 'action_failed', error.message.split('\n')[0]);
    }
    const out = await this.describe(tab);
    if (tab.dialog || image === undefined) return out;
    const file = this.keep(tab, 'png', image);
    return { ...out, file, bytes: image.length, width: image.readUInt32BE(16), height: image.readUInt32BE(20) };
  }

  async act(params) {
    const tab = this.tab(params);
    if (!KINDS.has(params.kind)) throw failure('invalid', `kind must be one of ${[...KINDS].join(', ')}`);
    if (tab.dialog) throw failure('dialog_open', 'a dialog is open; answer it with the dialog action first');
    try {
      await this.guarded(tab, () => this.perform(tab, params));
    } catch (error) {
      throw failure(error.code === 'invalid' ? 'invalid' : 'action_failed', error.message.split('\n')[0]);
    }
    return this.describe(tab);
  }

  locate(tab, ref) {
    if (!ref) throw failure('invalid', 'ref is required: take a snapshot and pass an element ref such as e5');
    return tab.page.getByRef(ref);
  }

  async perform(tab, { kind, ref, target, text, key, values, files }) {
    const timeout = ACTION_TIMEOUT_MS;
    switch (kind) {
      case 'click': return this.locate(tab, ref).click({ timeout });
      case 'hover': return this.locate(tab, ref).hover({ timeout });
      case 'type': return this.locate(tab, ref).fill(text ?? '', { timeout });
      case 'select': return this.locate(tab, ref).selectOption(values ?? [], { timeout });
      case 'upload': return this.locate(tab, ref).setInputFiles(files ?? [], { timeout });
      case 'drag': return this.locate(tab, ref).dragTo(this.locate(tab, target), { timeout });
      case 'press':
        if (!key) throw failure('invalid', 'key is required, for example Enter');
        return ref ? this.locate(tab, ref).press(key, { timeout }) : tab.page.keyboard.press(key);
    }
  }

  async wait(params) {
    const tab = this.tab(params);
    const timeout = Math.min(Math.max(Number(params.timeoutMs) || MAX_WAIT_MS, 1), MAX_WAIT_MS);
    try {
      await this.guarded(tab, async () => {
        if (params.text) await tab.page.getByText(params.text).first().waitFor({ timeout });
        else if (params.ref) await this.locate(tab, params.ref).waitFor({ timeout });
        else if (params.url) await tab.page.waitForURL(url => url.href.includes(params.url), { timeout });
        else if (params.idle) await tab.page.waitForLoadState('networkidle', { timeout });
        else throw failure('invalid', 'wait needs text, ref, url or idle');
      });
    } catch (error) {
      if (error.code === 'invalid') throw error;
      throw failure('timeout', error.message.split('\n')[0]);
    }
    return this.describe(tab);
  }

  async dialog(params) {
    const tab = this.tab(params);
    const dialog = tab.dialog;
    if (!dialog) throw failure('no_dialog', 'no dialog is open on this tab');
    tab.dialog = null;
    if (params.accept) await dialog.accept(params.text);
    else await dialog.dismiss();
    return this.describe(tab);
  }

  async resize(params) {
    const tab = this.tab(params);
    const width = Number(params.width);
    const height = Number(params.height);
    if (!(width >= 200 && width <= 3840 && height >= 200 && height <= 2160)) throw failure('invalid', 'width must be 200 to 3840 and height 200 to 2160');
    await this.guarded(tab, () => tab.page.setViewportSize({ width, height }));
    return this.describe(tab);
  }

  async list({ owner }) {
    const tabs = [];
    for (const tab of this.tabs.values()) {
      if (tab.owner === owner) tabs.push(await this.describe(tab));
    }
    return { tabs };
  }

  async close(params) {
    const tab = this.tab(params);
    this.tabs.delete(tab.id);
    await tab.page.close().catch(() => {});
    return { closed: [tab.id] };
  }

  async closeOwner({ owner }) {
    const closed = [...this.tabs.values()].filter(tab => tab.owner === owner).map(tab => tab.id);
    for (const id of closed) this.tabs.delete(id);
    const context = this.contexts.get(owner);
    this.contexts.delete(owner);
    await context?.close().catch(() => {});
    this.output.removeOwner(owner);
    return { closed };
  }

  forget() {
    this.tabs.clear();
    this.contexts.clear();
    this.output.clear();
  }
}
