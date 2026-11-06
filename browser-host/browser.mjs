import { execFile } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { promisify } from 'node:util';

const run = promisify(execFile);
const require = createRequire(import.meta.url);
const SHELL = 'chromium-headless-shell';
const INSTALL_TIMEOUT_MS = 10 * 60_000;
const PLAYWRIGHT = path.dirname(require.resolve('playwright-core/package.json'));

export function pinnedShell() {
  const { browsers } = JSON.parse(fs.readFileSync(path.join(PLAYWRIGHT, 'browsers.json'), 'utf8'));
  const entry = browsers.find(browser => browser.name === SHELL);
  return { revision: entry.revision, version: entry.browserVersion };
}

export function missingLibraries(ldd) {
  const missing = new Set();
  for (const line of ldd.split('\n')) {
    const match = line.match(/^\s*(\S+)\s+=>\s+not found/);
    if (match) missing.add(match[1]);
  }
  return [...missing].sort();
}

export class Browser {
  constructor({ browsers, node }) {
    this.browsers = browsers;
    this.node = node;
    this.shell = pinnedShell();
    this.cli = path.join(PLAYWRIGHT, 'cli.js');
    this.browser = null;
    this.starting = null;
    this.error = '';
  }

  installDir() {
    return path.join(this.browsers, `chromium_headless_shell-${this.shell.revision}`);
  }

  executable() {
    const dir = this.installDir();
    if (!fs.existsSync(path.join(dir, 'INSTALLATION_COMPLETE'))) return '';
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const candidate = path.join(dir, entry.name, 'chrome-headless-shell');
      if (entry.isDirectory() && fs.existsSync(candidate)) return candidate;
    }
    return '';
  }

  installCommand() {
    if (process.platform !== 'linux') return '';
    return `sudo ${this.node} ${this.cli} install-deps ${SHELL}`;
  }

  async checkLibraries(executable) {
    if (process.platform !== 'linux') return [];
    const { stdout } = await run('ldd', [executable]).catch(error => ({ stdout: error.stdout ?? '' }));
    return missingLibraries(stdout);
  }

  async status() {
    const executable = this.executable();
    const status = { version: this.shell.version, running: this.browser?.isConnected() ?? false };
    if (!executable) return { ...status, state: this.error ? 'failed' : 'absent', error: this.error || undefined };
    const missing = await this.checkLibraries(executable);
    if (missing.length > 0) return { ...status, state: 'missing_libraries', missing, install: this.installCommand() };
    return { ...status, state: this.error ? 'failed' : 'ready', error: this.error || undefined };
  }

  launch() {
    if (this.browser?.isConnected()) return Promise.resolve();
    this.starting ??= this.start().finally(() => { this.starting = null; });
    return this.starting;
  }

  async start() {
    this.error = '';
    try {
      if (!this.executable()) await this.install();
      const missing = await this.checkLibraries(this.executable());
      if (missing.length > 0) throw Object.assign(new Error(`Chromium needs system libraries: ${missing.join(', ')}`), { code: 'missing_libraries' });
      const { chromium } = await import('playwright-core');
      this.browser = await chromium.launch({ headless: true, executablePath: this.executable() });
      this.browser.on('disconnected', () => { this.browser = null; });
    } catch (error) {
      if (error.code !== 'missing_libraries') this.error = error.message;
      throw error;
    }
  }

  async install() {
    await run(this.node, [this.cli, 'install', '--no-progress', SHELL], {
      env: { ...process.env, PLAYWRIGHT_BROWSERS_PATH: this.browsers },
      timeout: INSTALL_TIMEOUT_MS,
      maxBuffer: 4 << 20,
    }).catch(error => {
      throw new Error(`download Chromium ${this.shell.version}: ${(error.stderr || error.message).trim().split('\n').pop()}`);
    });
  }

  async close() {
    const browser = this.browser;
    this.browser = null;
    await browser?.close().catch(() => {});
  }
}
