import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import test from "node:test"
import worker, { AppAttestChallenge, checkRegistration, issueChallenge, register, revoke, send, verifyAppAttestation } from "../src/index.js"

const productionAttestation = JSON.parse(readFileSync(new URL("fixtures/app-attest-production.json", import.meta.url), "utf8"))

class MemoryKV {
  values = new Map()
  async put(key, value) { this.values.set(key, value) }
  async get(key, type) {
    const value = this.values.get(key)
    return type === "json" && value ? JSON.parse(value) : value ?? null
  }
  async delete(key) { this.values.delete(key) }
}

class MemoryLimiter {
  constructor(limit = Infinity) { this.limitValue = limit }
  counts = new Map()
  async limit({ key }) {
    const count = (this.counts.get(key) || 0) + 1
    this.counts.set(key, count)
    return { success: count <= this.limitValue }
  }
}

class MemoryDurableStorage {
  values = new Map()
  alarm = null
  async get(key) { return this.values.get(key) }
  async put(key, value) { this.values.set(key, value) }
  async setAlarm(value) { this.alarm = value }
  async deleteAll() { this.values.clear() }
  async transaction(callback) { return callback(this) }
}

class MemoryChallengeNamespace {
  instances = new Map()
  getByName(id) {
    if (!this.instances.has(id)) {
      const storage = new MemoryDurableStorage()
      const object = new AppAttestChallenge({ storage })
      this.instances.set(id, { storage, object })
    }
    const { object } = this.instances.get(id)
    return { fetch: (url, init) => object.fetch(new Request(url, init)) }
  }
}

function environment(overrides = {}) {
  return {
    PUSH_DEVICES: new MemoryKV(),
    REGISTER_RATE_LIMITER: new MemoryLimiter(),
    SEND_RATE_LIMITER: new MemoryLimiter(),
    APP_ATTEST_CHALLENGES: new MemoryChallengeNamespace(),
    APPLE_TEAM_ID: "V8H6LQ9448",
    APP_ATTEST_BUNDLE_ID: "io.uebelacker.AppAttestExample",
    ...overrides
  }
}

function request(method, body) {
  return new Request("https://push.test/v1", { method, headers: { "content-type": "application/json" }, body: JSON.stringify(body) })
}

async function challenge(env, expiresAt = Date.now() + 60_000) {
  const challengeId = crypto.randomUUID()
  const challenge = Buffer.alloc(32, 7).toString("base64url")
  const response = await env.APP_ATTEST_CHALLENGES.getByName(challengeId).fetch("https://challenge.internal/issue", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ challenge, expiresAt })
  })
  assert.equal(response.status, 201)
  return { challengeId, challenge }
}

async function registrationBody(env, overrides = {}) {
  const issued = await challenge(env)
  return {
    deviceToken: "ab".repeat(32),
    environment: "sandbox",
    challengeId: issued.challengeId,
    keyId: Buffer.alloc(32, 3).toString("base64"),
    attestation: Buffer.from("attestation").toString("base64"),
    ...overrides
  }
}

const acceptAttestation = () => ({ environment: "production" })

test("registration issues a scoped capability and supports revocation", async () => {
  const env = environment()
  const response = await register(request("POST", await registrationBody(env)), env, acceptAttestation)
  assert.equal(response.status, 201)
  const registration = await response.json()
  assert.ok(registration.installationId)
  assert.ok(registration.credential)
  assert.ok(registration.expiresAt > Date.now() + 89 * 24 * 60 * 60 * 1000)
  assert.equal(JSON.stringify([...env.PUSH_DEVICES.values.values()]).includes(registration.credential), false)
  const denied = await revoke(request("DELETE", { installationId: registration.installationId, credential: "wrong" }), env)
  assert.equal(denied.status, 401)
  const revoked = await revoke(request("DELETE", registration), env)
  assert.equal(revoked.status, 200)
  assert.equal(env.PUSH_DEVICES.values.size, 0)
})

test("registration checks reject stale credentials", async () => {
  const env = environment()
  const registered = await register(request("POST", await registrationBody(env)), env, acceptAttestation)
  const registration = await registered.json()
  const makeCheck = () => new Request("https://push.test/v1/register/check", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(registration)
  })
  const valid = await worker.fetch(makeCheck(), env)
  assert.equal(valid.status, 200)
  await revoke(request("DELETE", registration), env)
  const stale = await worker.fetch(makeCheck(), env)
  assert.equal(stale.status, 401)
})

test("registration checks are rate limited", async () => {
  const env = environment({ REGISTER_RATE_LIMITER: new MemoryLimiter(0) })
  const response = await checkRegistration(request("POST", { installationId: "missing", credential: "missing" }), env)
  assert.equal(response.status, 429)
})

test("send rejects unknown capabilities before APNs", async () => {
  const env = environment()
  const response = await send(request("POST", { installationId: "missing", credential: "missing", title: "Agenthail", message: "Done" }), env)
  assert.equal(response.status, 401)
})

test("send invalidates registrations created before App Attest enforcement", async () => {
  const env = environment()
  const credential = "legacy-credential"
  env.PUSH_DEVICES.values.set("device:legacy", JSON.stringify({
    token: "ab".repeat(32),
    environment: "production",
    credentialHash: await crypto.subtle.digest("SHA-256", new TextEncoder().encode(credential)).then(value => Buffer.from(value).toString("base64url"))
  }))
  const response = await send(request("POST", { installationId: "legacy", credential, title: "Agenthail", message: "Done" }), env)
  assert.equal(response.status, 401)
  assert.equal(env.PUSH_DEVICES.values.has("device:legacy"), false)
})

test("registration rejects malformed APNs tokens", async () => {
  const env = environment()
  const response = await register(request("POST", await registrationBody(env, { deviceToken: "not-a-token" })), env, acceptAttestation)
  assert.equal(response.status, 400)
})

test("registration rejects oversized chunked JSON bodies", async () => {
  const env = environment()
  const response = await register(new Request("https://push.test/v1/register", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ deviceToken: "ab".repeat(32), padding: "x".repeat(25000) })
  }), env, acceptAttestation)
  assert.equal(response.status, 400)
})

test("registration accepts production-sized attestation envelopes", async () => {
  const env = environment()
  const body = await registrationBody(env, { attestation: Buffer.alloc(7000, 1).toString("base64") })
  const response = await register(request("POST", body), env, acceptAttestation)
  assert.equal(response.status, 201)
})

test("send cancels oversized streams without a content length", async () => {
  const env = environment()
  let pulls = 0
  let cancelled = false
  const body = new ReadableStream({
    pull(controller) {
      pulls += 1
      controller.enqueue(new Uint8Array(4096))
    },
    cancel() {
      cancelled = true
    }
  })
  const response = await send(new Request("https://push.test/v1/send", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body,
    duplex: "half"
  }), env)
  assert.equal(response.status, 401)
  assert.equal(cancelled, true)
  assert.ok(pulls <= 3)
})

test("registration rate limits repeated clients", async () => {
  const env = environment({ REGISTER_RATE_LIMITER: new MemoryLimiter(10) })
  for (let index = 0; index < 10; index += 1) {
    const body = await registrationBody(env)
    const response = await register(new Request("https://push.test/v1/register", {
      method: "POST",
      headers: { "content-type": "application/json", "cf-connecting-ip": "192.0.2.1" },
      body: JSON.stringify(body)
    }), env, acceptAttestation)
    assert.equal(response.status, 201)
  }
  const body = await registrationBody(env)
  const limited = await register(new Request("https://push.test/v1/register", {
    method: "POST",
    headers: { "content-type": "application/json", "cf-connecting-ip": "192.0.2.1" },
    body: JSON.stringify(body)
  }), env, acceptAttestation)
  assert.equal(limited.status, 429)
})

test("worker converts provider failures into a typed service error", async () => {
  const env = environment()
  const registered = await register(request("POST", await registrationBody(env)), env, acceptAttestation)
  const registration = await registered.json()
  const response = await worker.fetch(new Request("https://push.test/v1/send", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ ...registration, title: "Agenthail", message: "Done" })
  }), env)
  assert.equal(response.status, 503)
  assert.deepEqual(await response.json(), { error: "push_service_unavailable" })
})

test("health reports the relay protocol and capabilities", async () => {
  const response = await worker.fetch(new Request("https://push.test/health"), { RELAY_VERSION: "v1.2.3" })
  assert.equal(response.status, 200)
  const health = await response.json()
  assert.equal(health.version, "v1.2.3")
  assert.equal(health.protocol, 2)
  assert.ok(health.capabilities.includes("rate-limit"))
  assert.ok(health.capabilities.includes("app-attest"))
  assert.ok(health.capabilities.includes("registration-check"))
  assert.equal(health.ok, false)
  assert.deepEqual(health.appAttest, { required: true, configured: false })
  assert.deepEqual(health.apns, { configured: false })
  assert.deepEqual(health.rateLimits, { configured: false })
  const configured = await worker.fetch(new Request("https://push.test/health"), environment({
    APNS_KEY_P8: "private-key",
    APNS_KEY_ID: "key-id",
    APNS_TOPIC: "com.agenthail.ios"
  }))
  const configuredHealth = await configured.json()
  assert.equal(configuredHealth.ok, true)
  assert.deepEqual(configuredHealth.appAttest, { required: true, configured: true })
  assert.deepEqual(configuredHealth.apns, { configured: true })
  assert.deepEqual(configuredHealth.rateLimits, { configured: true })
})

test("production limiter rejects registration before storage", async () => {
  const env = {
    PUSH_DEVICES: new MemoryKV(),
    REGISTER_RATE_LIMITER: new MemoryLimiter(0),
    APP_ATTEST_CHALLENGES: new MemoryChallengeNamespace(),
    APPLE_TEAM_ID: "V8H6LQ9448",
    APP_ATTEST_BUNDLE_ID: "io.uebelacker.AppAttestExample"
  }
  const response = await register(new Request("https://push.test/v1/register", {
    method: "POST",
    headers: { "content-type": "application/json", "cf-connecting-ip": "192.0.2.2" },
    body: JSON.stringify({ deviceToken: "ab".repeat(32) })
  }), env)
  assert.equal(response.status, 429)
  assert.equal(env.PUSH_DEVICES.values.size, 0)
})

test("challenge endpoint fails closed when durable storage is missing", async () => {
  const response = await issueChallenge(new Request("https://push.test/v1/attest/challenge", {
    method: "POST",
    headers: { "cf-connecting-ip": "192.0.2.3" }
  }), environment({ APP_ATTEST_CHALLENGES: undefined }))
  assert.equal(response.status, 503)
  assert.deepEqual(await response.json(), { error: "app_attest_store_unavailable" })
})

test("challenge endpoint fails closed when app identity is missing", async () => {
  const response = await issueChallenge(new Request("https://push.test/v1/attest/challenge", {
    method: "POST",
    headers: { "cf-connecting-ip": "192.0.2.4" }
  }), environment({ APPLE_TEAM_ID: undefined }))
  assert.equal(response.status, 503)
  assert.deepEqual(await response.json(), { error: "app_attest_configuration_unavailable" })
})

test("registration fails closed when durable storage is missing", async () => {
  const env = environment()
  const body = await registrationBody(env)
  env.APP_ATTEST_CHALLENGES = undefined
  const response = await register(request("POST", body), env, acceptAttestation)
  assert.equal(response.status, 503)
  assert.deepEqual(await response.json(), { error: "app_attest_store_unavailable" })
})

test("registration rejects malformed attestations with a typed error", async () => {
  const env = environment()
  const response = await register(request("POST", await registrationBody(env)), env)
  assert.equal(response.status, 401)
  assert.deepEqual(await response.json(), { error: "app_attestation_rejected" })
})

test("verifier accepts an Apple production attestation through all validation steps", async () => {
  const result = await verifyAppAttestation({
    attestation: productionAttestation.attestation,
    challenge: Buffer.from(productionAttestation.challenge, "base64").toString("base64url"),
    keyId: productionAttestation.keyId,
    bundleIdentifier: "io.uebelacker.AppAttestExample",
    teamIdentifier: "V8H6LQ9448",
    allowDevelopmentEnvironment: false,
    now: Date.UTC(2024, 2, 1)
  })
  assert.ok(result.publicKeyPem.includes("BEGIN PUBLIC KEY"))
})

test("verifier rejects a mismatched one-time challenge", async () => {
  await assert.rejects(() => verifyAppAttestation({
    attestation: productionAttestation.attestation,
    challenge: Buffer.alloc(32, 9).toString("base64url"),
    keyId: productionAttestation.keyId,
    bundleIdentifier: "io.uebelacker.AppAttestExample",
    teamIdentifier: "V8H6LQ9448",
    allowDevelopmentEnvironment: false,
    now: Date.UTC(2024, 2, 1)
  }), /nonce mismatch/)
})

test("verifier rejects an expired Apple credential certificate", async () => {
  await assert.rejects(() => verifyAppAttestation({
    attestation: productionAttestation.attestation,
    challenge: Buffer.from(productionAttestation.challenge, "base64").toString("base64url"),
    keyId: productionAttestation.keyId,
    bundleIdentifier: "io.uebelacker.AppAttestExample",
    teamIdentifier: "V8H6LQ9448",
    allowDevelopmentEnvironment: false,
    now: Date.UTC(2026, 0, 1)
  }), /certificate outside validity period/)
})

test("registration rejects expired challenges", async () => {
  const env = environment()
  const body = await registrationBody(env)
  const stored = env.APP_ATTEST_CHALLENGES.instances.get(body.challengeId).storage.values.get("challenge")
  env.APP_ATTEST_CHALLENGES.instances.get(body.challengeId).storage.values.set("challenge", { ...stored, expiresAt: Date.now() - 1 })
  const response = await register(request("POST", body), env, acceptAttestation)
  assert.equal(response.status, 410)
  assert.deepEqual(await response.json(), { error: "challenge_expired" })
})

test("registration rejects replayed challenges", async () => {
  const env = environment()
  const body = await registrationBody(env)
  const first = await register(request("POST", body), env, acceptAttestation)
  assert.equal(first.status, 201)
  const replay = await register(request("POST", body), env, acceptAttestation)
  assert.equal(replay.status, 409)
  assert.deepEqual(await replay.json(), { error: "challenge_replayed" })
  assert.equal(env.PUSH_DEVICES.values.size, 1)
})
