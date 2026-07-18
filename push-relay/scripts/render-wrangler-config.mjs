import { readFile, writeFile } from "node:fs/promises"
import { pathToFileURL } from "node:url"

export function renderWranglerConfig(source, kvNamespaceId = "") {
  const id = kvNamespaceId.trim()
  if (!id) return source
  if (!/^[a-f0-9]{32}$/i.test(id)) throw new Error("CLOUDFLARE_PUSH_DEVICES_KV_ID must be a 32-character hexadecimal namespace ID")
  const marker = '[[kv_namespaces]]\nbinding = "PUSH_DEVICES"'
  if (source.split(marker).length !== 2) throw new Error("PUSH_DEVICES binding was not found exactly once")
  return source.replace(marker, `${marker}\nid = "${id}"`)
}

async function main() {
  const [input, output] = process.argv.slice(2)
  if (!input || !output) throw new Error("usage: render-wrangler-config.mjs <input> <output>")
  const source = await readFile(input, "utf8")
  await writeFile(output, renderWranglerConfig(source, process.env.CLOUDFLARE_PUSH_DEVICES_KV_ID || ""))
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  })
}
