import crypto from 'node:crypto';
import fs from 'node:fs';
import { register } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { DatabaseSync } from 'node:sqlite';
import { callTool } from './mcp.mjs';

register('./hooks.mjs', import.meta.url);

const MIN_JOB_SECONDS = 60;
const NAME = /^[a-z][a-z0-9_-]{0,63}$/;

export function tablePrefix(id) {
  return id.replaceAll('-', '_') + '_';
}

function failure(code, message) {
  return Object.assign(new Error(message), { code });
}

function log(id, message) {
  process.stderr.write(`extension ${id}: ${message}\n`);
}

class Loaded {
  constructor(spec) {
    this.id = spec.id;
    this.dir = spec.dir;
    this.daemon = { url: spec.mcpURL, token: spec.token };
    this.state = 'loading';
    this.error = '';
    this.actions = new Map();
    this.jobs = new Map();
    this.handlers = [];
    this.dispose = null;
    this.lastEventError = '';
  }

  summary() {
    return {
      id: this.id,
      state: this.state,
      error: this.error || undefined,
      actions: [...this.actions.keys()].sort(),
      jobs: [...this.jobs.values()].map(job => ({ name: job.name, everySeconds: job.everySeconds })),
      events: this.handlers.length > 0,
      lastEventError: this.lastEventError || undefined,
    };
  }

  api(db, writes) {
    const self = this;
    const registering = () => {
      if (self.state !== 'loading') throw failure('invalid', 'register actions, jobs and handlers while the extension loads');
    };
    return Object.freeze({
      id: self.id,
      dir: self.dir,
      db,
      table: name => {
        if (!NAME.test(name)) throw failure('invalid', `table name ${JSON.stringify(name)} must match ${NAME}`);
        return tablePrefix(self.id) + name.replaceAll('-', '_');
      },
      on(type, handler) {
        registering();
        if (typeof type !== 'string' || typeof handler !== 'function') throw failure('invalid', 'on(type, handler)');
        self.handlers.push({ type, handler });
      },
      action(name, handler) {
        registering();
        if (!NAME.test(name) || typeof handler !== 'function') throw failure('invalid', `action(name, handler): name must match ${NAME}`);
        if (self.actions.has(name)) throw failure('invalid', `action ${name} is registered twice`);
        self.actions.set(name, handler);
      },
      job({ name, everySeconds, run }) {
        registering();
        if (!NAME.test(name) || typeof run !== 'function') throw failure('invalid', `job({name, everySeconds, run}): name must match ${NAME}`);
        if (!Number.isInteger(everySeconds) || everySeconds < MIN_JOB_SECONDS) throw failure('invalid', `job ${name}: everySeconds must be a whole number of at least ${MIN_JOB_SECONDS}`);
        if (self.jobs.has(name)) throw failure('invalid', `job ${name} is registered twice`);
        self.jobs.set(name, { name, everySeconds, run });
      },
      async call(tool, args = {}) {
        const input = { ...args };
        if (writes.has(tool) && !('clientRequestId' in input)) input.clientRequestId = crypto.randomUUID();
        return callTool(self.daemon, tool, input);
      },
      log: (...parts) => log(self.id, parts.map(part => (typeof part === 'string' ? part : JSON.stringify(part))).join(' ')),
    });
  }
}

export class Extensions {
  constructor({ store }) {
    this.storePath = store;
    this.db = null;
    this.loaded = new Map();
    this.generation = 0;
    this.writeTools = new Set();
  }

  database() {
    if (!this.db) {
      fs.mkdirSync(path.dirname(this.storePath), { recursive: true, mode: 0o700 });
      this.db = new DatabaseSync(this.storePath);
      this.db.exec('PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;');
      fs.chmodSync(this.storePath, 0o600);
    }
    return this.db;
  }

  status() {
    return { extensions: [...this.loaded.values()].map(extension => extension.summary()) };
  }

  async unload() {
    for (const extension of this.loaded.values()) {
      extension.state = 'unloaded';
      if (typeof extension.dispose === 'function') {
        try {
          await extension.dispose();
        } catch (error) {
          log(extension.id, `cleanup failed: ${error?.stack ?? error}`);
        }
      }
    }
    this.loaded.clear();
  }

  async load({ extensions = [], writeTools = [] }) {
    await this.unload();
    this.writeTools = new Set(writeTools);
    this.generation += 1;
    for (const spec of extensions) {
      const extension = new Loaded(spec);
      this.loaded.set(extension.id, extension);
      try {
        const entry = path.join(extension.dir, 'server.ts');
        const url = pathToFileURL(entry);
        url.searchParams.set('agenthail-load', String(this.generation));
        url.searchParams.set('agenthail-root', pathToFileURL(extension.dir).pathname + '/');
        const module = await import(url.href);
        if (typeof module.default !== 'function') throw failure('invalid', 'server.ts must export a default function that receives the extension API');
        const dispose = await module.default(extension.api(this.database(), this.writeTools));
        extension.dispose = typeof dispose === 'function' ? dispose : null;
        extension.state = 'loaded';
      } catch (error) {
        extension.state = 'failed';
        extension.error = String(error?.message ?? error);
        extension.actions.clear();
        extension.jobs.clear();
        extension.handlers = [];
        log(extension.id, `load failed: ${error?.stack ?? error}`);
      }
    }
    return this.status();
  }

  ready(id) {
    const extension = this.loaded.get(id);
    if (!extension) throw failure('not_found', `no extension ${id} is loaded`);
    if (extension.state !== 'loaded') throw failure('unavailable', `extension ${id} failed to load: ${extension.error}`);
    return extension;
  }

  async action({ id, name, input, caller }) {
    const extension = this.ready(id);
    const handler = extension.actions.get(name);
    if (!handler) throw failure('not_found', `extension ${id} has no action ${name}`);
    try {
      const result = await handler(input ?? {}, { caller: caller ?? {} });
      return { result: result ?? null };
    } catch (error) {
      if (error?.code) throw error;
      log(id, `action ${name} failed: ${error?.stack ?? error}`);
      throw failure('action_failed', String(error?.message ?? error));
    }
  }

  async job({ id, name }) {
    const extension = this.ready(id);
    const job = extension.jobs.get(name);
    if (!job) throw failure('not_found', `extension ${id} has no job ${name}`);
    try {
      await job.run({ now: new Date() });
      return {};
    } catch (error) {
      log(id, `job ${name} failed: ${error?.stack ?? error}`);
      throw failure('job_failed', String(error?.message ?? error));
    }
  }

  deliver({ events = [], to = [] }) {
    for (const id of to) {
      const extension = this.loaded.get(id);
      if (!extension || extension.state !== 'loaded') continue;
      for (const event of events) {
        for (const { type, handler } of extension.handlers) {
          if (type !== '*' && type !== event.type) continue;
          Promise.resolve()
            .then(() => handler(event))
            .catch(error => {
              extension.lastEventError = `${event.type}: ${error?.message ?? error}`;
              log(id, `handler for ${event.type} failed: ${error?.stack ?? error}`);
            });
        }
      }
    }
    return {};
  }

  async close() {
    await this.unload();
    this.db?.close();
    this.db = null;
  }
}
