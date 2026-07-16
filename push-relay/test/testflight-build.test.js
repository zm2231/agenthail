import assert from "node:assert/strict"
import { generateKeyPairSync, verify } from "node:crypto"
import test from "node:test"
import { appLookupURL, buildLookupURL, createAppStoreToken, waitForProcessedBuild } from "../scripts/wait-testflight-build.mjs"

const keys = generateKeyPairSync("ec", { namedCurve: "P-256" })
const privateKey = keys.privateKey.export({ type: "pkcs8", format: "pem" })

function response(status, body) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } })
}

function harness(replies) {
  let clock = 1_000_000
  const requests = []
  return {
    requests,
    options: {
      issuerId: "issuer",
      keyId: "KEY123",
      privateKey,
      bundleId: "com.agenthail.ios",
      buildNumber: "42",
      marketingVersion: "v0.2.0",
      timeoutMs: 5000,
      intervalMs: 1000,
      now: () => clock,
      sleep: async ms => { clock += ms },
      onStatus: () => {},
      fetchImpl: async url => {
        requests.push(String(url))
        const next = replies.shift()
        if (!next) throw new Error("unexpected request")
        if (next instanceof Error) throw next
        return next
      }
    }
  }
}

test("creates a valid App Store Connect token", () => {
  const token = createAppStoreToken({ issuerId: "issuer", keyId: "KEY123", privateKey, now: 1_000_000 })
  const [header, payload, signature] = token.split(".")
  assert.deepEqual(JSON.parse(Buffer.from(header, "base64url")), { alg: "ES256", kid: "KEY123", typ: "JWT" })
  assert.equal(JSON.parse(Buffer.from(payload, "base64url")).aud, "appstoreconnect-v1")
  assert.equal(verify("sha256", Buffer.from(`${header}.${payload}`), { key: keys.publicKey, dsaEncoding: "ieee-p1363" }, Buffer.from(signature, "base64url")), true)
})

test("build query binds app, version, platform, and build number", () => {
  const appURL = appLookupURL("com.agenthail.ios", "https://example.test")
  assert.equal(appURL.searchParams.get("filter[bundleId]"), "com.agenthail.ios")
  const buildURL = buildLookupURL({ appId: "app-1", buildNumber: "42", marketingVersion: "v0.2.0" }, "https://example.test")
  assert.equal(buildURL.searchParams.get("filter[app]"), "app-1")
  assert.equal(buildURL.searchParams.get("filter[version]"), "42")
  assert.equal(buildURL.searchParams.get("filter[preReleaseVersion.platform]"), "IOS")
  assert.equal(buildURL.searchParams.get("filter[preReleaseVersion.version]"), "0.2.0")
})

test("waits for the exact build to become valid", async () => {
  const run = harness([
    response(200, { data: [{ id: "app-1" }] }),
    response(200, { data: [] }),
    response(200, { data: [{ id: "build-1", attributes: { processingState: "PROCESSING", expired: false } }] }),
    response(200, { data: [{ id: "build-1", attributes: { processingState: "VALID", expired: false } }] })
  ])
  const build = await waitForProcessedBuild(run.options)
  assert.equal(build.id, "build-1")
  assert.equal(run.requests.length, 4)
})

test("fails closed on invalid processing", async () => {
  const run = harness([
    response(200, { data: [{ id: "app-1" }] }),
    response(200, { data: [{ id: "build-1", attributes: { processingState: "INVALID", expired: false } }] })
  ])
  await assert.rejects(waitForProcessedBuild(run.options), /ended in INVALID/)
})

test("retries transient API failures", async () => {
  const run = harness([
    response(200, { data: [{ id: "app-1" }] }),
    response(503, { errors: [{ detail: "temporarily unavailable" }] }),
    response(200, { data: [{ id: "build-1", attributes: { processingState: "VALID", expired: false } }] })
  ])
  const build = await waitForProcessedBuild(run.options)
  assert.equal(build.id, "build-1")
})

test("retries transient network failures", async () => {
  const run = harness([
    response(200, { data: [{ id: "app-1" }] }),
    new Error("connection reset"),
    response(200, { data: [{ id: "build-1", attributes: { processingState: "VALID", expired: false } }] })
  ])
  const build = await waitForProcessedBuild(run.options)
  assert.equal(build.id, "build-1")
})

test("times out when the upload never appears", async () => {
  const run = harness([
    response(200, { data: [{ id: "app-1" }] }),
    response(200, { data: [] }),
    response(200, { data: [] }),
    response(200, { data: [] }),
    response(200, { data: [] }),
    response(200, { data: [] })
  ])
  await assert.rejects(waitForProcessedBuild(run.options), /Timed out/)
})
