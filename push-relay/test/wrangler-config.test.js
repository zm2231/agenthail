import test from "node:test"
import assert from "node:assert/strict"
import { renderWranglerConfig } from "../scripts/render-wrangler-config.mjs"

const source = `name = "agenthail-push"

[[kv_namespaces]]
binding = "PUSH_DEVICES"
`

test("portable config leaves KV provisioning to Wrangler", () => {
  assert.equal(renderWranglerConfig(source), source)
})

test("release config binds the existing production namespace", () => {
  const id = "0123456789abcdef0123456789abcdef"
  assert.match(renderWranglerConfig(source, id), new RegExp(`id = "${id}"`))
})

test("release config rejects malformed namespace IDs", () => {
  assert.throws(() => renderWranglerConfig(source, "not-an-id"), /32-character hexadecimal/)
})
