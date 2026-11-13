import crypto from 'node:crypto';
import fs from 'node:fs';
import { register } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { DatabaseSync } from 'node:sqlite';
import { callTool, fetchMount } from './mcp.mjs';

register('./hooks.mjs', import.meta.url);

const MIN_JOB_SECONDS = 60;
const MIN_SOURCE_SECONDS = 10;
const MAX_SOURCE_TIMEOUT_SECONDS = 30;
const MAX_SOURCE_BYTES = 512 << 10;
const NAME = /^[a-z][a-z0-9_-]{0,63}$/;

export function tablePrefix(id) {
  return id.replaceAll('-', '_') + '_';
}

export function sourceTable(id) {
  return tablePrefix(id) + '_sources';
}

function failure(code, message) {
  return Object.assign(new Error(message), { code });
}

function log(id, message) {
  process.stderr.write(`extension ${id}: ${message}\n`);
}

function ensureSourceTable(db, id) {
  db.exec(`CREATE TABLE IF NOT EXISTS ${sourceTable(id)} (name TEXT PRIMARY KEY, value TEXT, at INTEGER, error TEXT, error_at INTEGER, took_ms INTEGER)`);
}

// A failed run keeps the last good value, so a page goes on showing it
// beside the error. A collector that outlives its timeout still holds the
// source, so no second one starts beside it.
async function collectSource(db, id, name, timeoutSeconds, collect, held) {
  ensureSourceTable(db, id);
  const table = sourceTable(id);
  const started = Date.now();
  const abort = new AbortController();
  let timer;
  try {
    const deadline = new Promise((_, fail) => {
      timer = setTimeout(() => {
        abort.abort();
        fail(new Error(`no answer in ${timeoutSeconds} s`));
      }, timeoutSeconds * 1000);
    });
    if (held.run) throw new Error(`the collection started ${Math.round((started - held.since) / 1000)} s ago has not finished`);
    const run = Promise.resolve().then(() => collect({ signal: abort.signal, now: new Date() }));
    held.run = run;
    held.since = started;
    run.then(() => {}, () => {}).finally(() => {
      if (held.run === run) held.run = null;
    });
    const value = await Promise.race([run, deadline]);
    const encoded = JSON.stringify(value ?? null);
    const bytes = Buffer.byteLength(encoded, 'utf8');
    if (bytes > MAX_SOURCE_BYTES) throw new Error(`the value is ${bytes} bytes, over the ${MAX_SOURCE_BYTES}-byte limit`);
    db.prepare(`INSERT INTO ${table} (name, value, at, error, error_at, took_ms) VALUES (?, ?, ?, NULL, NULL, ?)
      ON CONFLICT(name) DO UPDATE SET value = excluded.value, at = excluded.at, error = NULL, error_at = NULL, took_ms = excluded.took_ms`).run(name, encoded, Date.now(), Date.now() - started);
  } catch (error) {
    const message = String(error?.message ?? error);
    db.prepare(`INSERT INTO ${table} (name, error, error_at, took_ms) VALUES (?, ?, ?, ?)
      ON CONFLICT(name) DO UPDATE SET error = excluded.error, error_at = excluded.error_at, took_ms = excluded.took_ms`).run(name, message, Date.now(), Date.now() - started);
    throw failure('source_failed', `source ${name}: ${message}`);
  } finally {
    clearTimeout(timer);
  }
}

function sourceState(row, withValue) {
  const state = { name: row.name, state: row.error != null ? 'failed' : row.at != null ? 'ok' : 'collecting' };
  if (row.at != null) state.at = row.at;
  if (row.error != null) {
    state.error = row.error;
    state.errorAt = row.error_at;
  }
  if (row.took_ms != null) state.tookMs = row.took_ms;
  if (withValue && row.value != null) state.value = JSON.parse(row.value);
  return state;
}

function readSources(db, id, withValue, names) {
  ensureSourceTable(db, id);
  const rows = new Map(db.prepare(`SELECT name, value, at, error, error_at, took_ms FROM ${sourceTable(id)}`).all().map(row => [row.name, row]));
  const out = {};
  for (const name of names ?? rows.keys()) out[name] = sourceState(rows.get(name) ?? { name }, withValue);
  return out;
}

function callAs(daemon, writes, tool, args = {}) {
  const input = { ...args };
  if (writes.has(tool) && !('clientRequestId' in input)) input.clientRequestId = crypto.randomUUID();
  return callTool(daemon, tool, input);
}

class Loaded {
  constructor(spec) {
    this.id = spec.id;
    this.dir = spec.dir;
    this.daemon = { url: spec.mcpURL, fetchURL: spec.fetchURL, token: spec.token };
    this.state = 'loading';
    this.error = '';
    this.actions = new Map();
    this.jobs = new Map();
    this.tools = new Map();
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
      jobs: [...this.jobs.values()].map(job => ({ name: job.name, everySeconds: job.everySeconds, source: job.source || undefined })),
      tools: [...this.tools.values()].map(({ name, description, inputSchema }) => ({ name, description, inputSchema })),
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
        if (self.jobs.has(name)) throw failure('invalid', `${name} is registered twice as a job or source`);
        self.jobs.set(name, { name, everySeconds, run });
      },
      source({ name, everySeconds = 60, timeoutSeconds, collect }) {
        registering();
        if (!NAME.test(name) || typeof collect !== 'function') throw failure('invalid', `source({name, everySeconds, timeoutSeconds, collect}): name must match ${NAME}`);
        if (!Number.isInteger(everySeconds) || everySeconds < MIN_SOURCE_SECONDS) throw failure('invalid', `source ${name}: everySeconds must be a whole number of at least ${MIN_SOURCE_SECONDS}`);
        const timeout = timeoutSeconds ?? Math.min(MAX_SOURCE_TIMEOUT_SECONDS, everySeconds - 1);
        if (!Number.isInteger(timeout) || timeout < 1 || timeout >= everySeconds || timeout > MAX_SOURCE_TIMEOUT_SECONDS) {
          throw failure('invalid', `source ${name}: timeoutSeconds must be a whole number from 1 to ${MAX_SOURCE_TIMEOUT_SECONDS} and below everySeconds`);
        }
        if (self.jobs.has(name)) throw failure('invalid', `${name} is registered twice as a job or source`);
        const held = { run: null, since: 0 };
        self.jobs.set(name, { name, everySeconds, source: true, run: () => collectSource(db, self.id, name, timeout, collect, held) });
      },
      sources: () => readSources(db, self.id, true),
      tool({ name, description, inputSchema = { type: 'object' }, run }) {
        registering();
        if (!NAME.test(name) || typeof run !== 'function') throw failure('invalid', `tool({name, description, inputSchema, run}): name must match ${NAME}`);
        if (typeof description !== 'string' || description.trim() === '') throw failure('invalid', `tool ${name} needs a description`);
        if (inputSchema === null || typeof inputSchema !== 'object' || Array.isArray(inputSchema) || inputSchema.type !== 'object') {
          throw failure('invalid', `tool ${name}: inputSchema must be a JSON Schema object with type "object"`);
        }
        if (self.tools.has(name)) throw failure('invalid', `tool ${name} is registered twice`);
        self.tools.set(name, { name, description: description.trim(), inputSchema, run });
      },
      call: (tool, args) => callAs(self.daemon, writes, tool, args),
      fetch: (mount, path, init) => fetchMount(self.daemon, mount, path, init),
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
        extension.tools.clear();
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

  sources({ id, name }) {
    const extension = this.ready(id);
    const declared = [...extension.jobs.values()].filter(job => job.source);
    if (name !== undefined) {
      const job = declared.find(job => job.name === name);
      if (!job) throw failure('not_found', `extension ${id} has no source ${name}`);
      return { source: { ...readSources(this.database(), id, true, [name])[name], everySeconds: job.everySeconds } };
    }
    const states = readSources(this.database(), id, false, declared.map(job => job.name));
    return { sources: declared.map(job => ({ ...states[job.name], everySeconds: job.everySeconds })) };
  }

  // A tool call carries a token that acts for the calling session as far as
  // this extension may, so ctx.ah reaches only what both of them could.
  async tool({ id, name, input, caller, token }) {
    const extension = this.ready(id);
    const tool = extension.tools.get(name);
    if (!tool) throw failure('not_found', `extension ${id} has no tool ${name}`);
    const daemon = { ...extension.daemon, token };
    const ah = Object.freeze({ call: (toolName, args) => callAs(daemon, this.writeTools, toolName, args) });
    try {
      const result = await tool.run(input ?? {}, { caller: caller ?? {}, ah });
      return { result: result ?? null };
    } catch (error) {
      if (error?.code) throw error;
      log(id, `tool ${name} failed: ${error?.stack ?? error}`);
      throw failure('tool_failed', String(error?.message ?? error));
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
