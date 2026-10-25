import fs from "node:fs/promises";
import vm from "node:vm";
import { spawnSync } from "node:child_process";

if (!vm.SourceTextModule) {
  const result = spawnSync(process.execPath, ["--experimental-vm-modules", ...process.execArgv, ...process.argv.slice(1)], { stdio: "inherit" });
  process.exit(result.status ?? 1);
}

export async function dashboardModules(names, globals = {}, { dynamicImport } = {}) {
  const context = vm.isContext(globals) ? globals : vm.createContext(globals);
  const cache = new Map();
  const mocks = new Map(Object.entries(globals));
  const base = new URL("../internal/daemon/dashboard/js/", import.meta.url);
  async function read(url) {
    if (cache.has(url.href)) return cache.get(url.href);
    const source = await fs.readFile(url, "utf8");
    if (cache.has(url.href)) return cache.get(url.href);
    const module = new vm.SourceTextModule(source, {
      context, identifier: url.href,
      importModuleDynamically: async (specifier, parent) => {
        if (dynamicImport) return dynamicImport(specifier, parent, context);
        const imported = await read(new URL(specifier, parent.identifier));
        if (imported.status === "unlinked") await imported.link(link);
        if (imported.status === "linked") await imported.evaluate();
        return imported;
      },
    });
    cache.set(url.href, module);
    return module;
  }
  async function link(specifier, parent) {
    const url = new URL(specifier, parent.identifier);
    const module = await read(url);
    const exported = [...(await fs.readFile(url, "utf8")).matchAll(/^export (?:async )?(?:function|const|let) (\w+|\$)/gm)].map(match => match[1]);
    const mocked = exported.filter(name => mocks.has(name));
    if (!mocked.length) return module;
    const key = `mock:${url.href}`;
    if (cache.has(key)) return cache.get(key);
    const wrapper = new vm.SourceTextModule(`import * as actual from ${JSON.stringify(url.href)};\n${exported.map(name => `export const ${name} = ${mocks.has(name) ? `globalThis[${JSON.stringify(name)}]` : `actual.${name}`};`).join("\n")}`, { context, identifier: key });
    cache.set(key, wrapper);
    return wrapper;
  }
  const entry = new vm.SourceTextModule(names.map((name, index) => `export * as module${index} from "${new URL(`${name}.js`, base).href}";`).join("\n"), { context, identifier: new URL("test-entry.js", base).href });
  await entry.link((specifier, parent) => (parent.identifier.startsWith("mock:") || parent.identifier.endsWith("/test-entry.js")) ? read(new URL(specifier)) : link(specifier, parent));
  await entry.evaluate();
  const exports = Object.assign({}, ...Object.values(entry.namespace));
  Object.assign(globals, exports);
  return Object.fromEntries(names.map((name, index) => [name, entry.namespace[`module${index}`]]));
}
