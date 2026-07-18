import test from "node:test"
import assert from "node:assert/strict"
import { ensureKVNamespace } from "../scripts/ensure-kv-namespace.mjs"

const existingID = "0123456789abcdef0123456789abcdef"
const createdID = "fedcba9876543210fedcba9876543210"

function response(result, status = 200, success = true) {
  return new Response(JSON.stringify({ success, result, errors: success ? [] : [{ message: "denied" }] }), {
    status,
    headers: { "content-type": "application/json" }
  })
}

test("reuses the exact existing namespace", async () => {
  const calls = []
  const id = await ensureKVNamespace({ accountID: "account", token: "token" }, async (url, options) => {
    calls.push({ url, options })
    return response([{ id: existingID, title: "agenthail-push-devices" }])
  })
  assert.equal(id, existingID)
  assert.equal(calls.length, 1)
  assert.match(calls[0].url, /per_page=1000$/)
  assert.equal(calls[0].options.headers.authorization, "Bearer token")
})

test("creates a missing namespace once", async () => {
  const calls = []
  const id = await ensureKVNamespace({ accountID: "account", token: "token" }, async (url, options) => {
    calls.push({ url, options })
    if (calls.length === 1) return response([])
    return response({ id: createdID, title: "agenthail-push-devices" })
  })
  assert.equal(id, createdID)
  assert.equal(calls.length, 2)
  assert.equal(calls[1].options.method, "POST")
  assert.deepEqual(JSON.parse(calls[1].options.body), { title: "agenthail-push-devices" })
})

test("fails on duplicate names, malformed IDs, and API errors", async () => {
  await assert.rejects(
    ensureKVNamespace({ accountID: "account", token: "token" }, async () => response([{ id: existingID, title: "agenthail-push-devices" }, { id: createdID, title: "agenthail-push-devices" }])),
    /multiple Cloudflare KV namespaces/
  )
  await assert.rejects(
    ensureKVNamespace({ accountID: "account", token: "token" }, async () => response([{ id: "bad", title: "agenthail-push-devices" }])),
    /invalid KV namespace ID/
  )
  await assert.rejects(
    ensureKVNamespace({ accountID: "account", token: "token" }, async () => response(null, 403, false)),
    /denied/
  )
})

test("bounds stalled Cloudflare requests", async () => {
  await assert.rejects(
    ensureKVNamespace({ accountID: "account", token: "token", timeoutMs: 5 }, async () => new Promise(() => {})),
    /timed out after 5ms/
  )
})
