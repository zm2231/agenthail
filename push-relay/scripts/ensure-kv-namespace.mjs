import { pathToFileURL } from "node:url"

const namespaceIDPattern = /^[a-f0-9]{32}$/i

function requireValue(value, name) {
  const result = String(value || "").trim()
  if (!result) throw new Error(`${name} is required`)
  return result
}

async function cloudflareRequest(url, token, options = {}, fetcher = fetch, timeoutMs = 20000) {
  const controller = new AbortController()
  let timeout
  const request = fetcher(url, {
    ...options,
    signal: controller.signal,
    headers: {
      authorization: `Bearer ${token}`,
      "content-type": "application/json",
      ...(options.headers || {})
    }
  })
  const deadline = new Promise((_, reject) => {
    timeout = setTimeout(() => {
      controller.abort()
      reject(new Error(`Cloudflare KV request timed out after ${timeoutMs}ms`))
    }, timeoutMs)
  })
  const response = await Promise.race([request, deadline]).finally(() => clearTimeout(timeout))
  const payload = await response.json()
  if (!response.ok || payload.success !== true) {
    const message = payload.errors?.map(error => error.message).filter(Boolean).join("; ") || `HTTP ${response.status}`
    throw new Error(`Cloudflare KV request failed: ${message}`)
  }
  return payload.result
}

export async function ensureKVNamespace(options, fetcher = fetch) {
  const accountID = requireValue(options.accountID, "CLOUDFLARE_ACCOUNT_ID")
  const token = requireValue(options.token, "CLOUDFLARE_API_TOKEN")
  const title = requireValue(options.title || "agenthail-push-devices", "namespace title")
  const timeoutMs = options.timeoutMs || 20000
  const baseURL = String(options.baseURL || "https://api.cloudflare.com/client/v4").replace(/\/+$/, "")
  const endpoint = `${baseURL}/accounts/${encodeURIComponent(accountID)}/storage/kv/namespaces`
  const namespaces = await cloudflareRequest(`${endpoint}?per_page=1000`, token, {}, fetcher, timeoutMs)
  const matches = namespaces.filter(namespace => namespace.title === title)
  if (matches.length > 1) throw new Error(`multiple Cloudflare KV namespaces are named ${title}`)
  const id = matches[0]?.id || (await cloudflareRequest(endpoint, token, { method: "POST", body: JSON.stringify({ title }) }, fetcher, timeoutMs)).id
  if (!namespaceIDPattern.test(String(id || ""))) throw new Error("Cloudflare returned an invalid KV namespace ID")
  return id
}

async function main() {
  const id = await ensureKVNamespace({
    accountID: process.env.CLOUDFLARE_ACCOUNT_ID,
    token: process.env.CLOUDFLARE_API_TOKEN,
    title: process.env.CLOUDFLARE_PUSH_DEVICES_KV_TITLE,
    baseURL: process.env.CLOUDFLARE_API_BASE_URL
  })
  process.stdout.write(`${id}\n`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  })
}
