import { randomBytes } from 'node:crypto';
import { clipText, encodedBytes, excerpt } from './output.mjs';

const MIN_LIMIT = 1 << 10;
const URL_BYTES = 2 << 10;
const TITLE_BYTES = 512;
const MESSAGE_BYTES = 1 << 10;
const ACTION_TIMEOUT_MS = 10_000;
const NAVIGATE_TIMEOUT_MS = 30_000;
const MAX_WAIT_MS = 30_000;
const KINDS = new Set(['click', 'type', 'select', 'hover', 'drag', 'press', 'scroll', 'upload']);

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

  async snapshot(params) {
    const tab = this.tab(params);
    const limit = Number(params.limit);
    if (!(limit >= MIN_LIMIT)) throw failure('invalid', `limit must be at least ${MIN_LIMIT} bytes`);
    let tree;
    await this.guarded(tab, async () => {
      tree = await tab.page.ariaSnapshot({ mode: 'ai', timeout: ACTION_TIMEOUT_MS });
    });
    const out = await this.describe(tab);
    if (tab.dialog || tree === undefined) return out;
    const file = this.keep(tab, 'snapshot.yml', tree);
    const bytes = Buffer.byteLength(tree);
    const lines = tree.split('\n').length;
    const full = `The whole snapshot is in ${file} (${bytes} bytes, ${lines} lines); read it there. Its refs are valid until your next snapshot.`;
    const rest = { ...out, file, bytes, lines, truncated: true, full, snapshot: '' };
    const { text, truncated } = excerpt(tree, limit - encodedBytes(JSON.stringify(rest)));
    return truncated ? { ...rest, snapshot: text } : { ...out, file, bytes, lines, truncated: false, snapshot: text };
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

  async perform(tab, { kind, ref, target, text, key, values, files, deltaY }) {
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
      case 'scroll':
        return ref ? this.locate(tab, ref).scrollIntoViewIfNeeded({ timeout }) : tab.page.mouse.wheel(0, deltaY ?? 600);
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
