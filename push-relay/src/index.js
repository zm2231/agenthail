import * as asn1js from "asn1js"
import { Buffer } from "node:buffer"
import { createHash, X509Certificate } from "node:crypto"
import cbor from "cbor"
import * as pkijs from "pkijs"

const encoder = new TextEncoder()
const decoder = new TextDecoder()
let cachedProviderToken = null
const registrationTTLSeconds = 90 * 24 * 60 * 60
const challengeTTLMilliseconds = 5 * 60 * 1000
const appAttestNonceOID = "1.2.840.113635.100.8.2"
const appAttestRoot = new X509Certificate("-----BEGIN CERTIFICATE-----\nMIICITCCAaegAwIBAgIQC/O+DvHN0uD7jG5yH2IXmDAKBggqhkjOPQQDAzBSMSYwJAYDVQQDDB1BcHBsZSBBcHAgQXR0ZXN0YXRpb24gUm9vdCBDQTETMBEGA1UECgwKQXBwbGUgSW5jLjETMBEGA1UECAwKQ2FsaWZvcm5pYTAeFw0yMDAzMTgxODMyNTNaFw00NTAzMTUwMDAwMDBaMFIxJjAkBgNVBAMMHUFwcGxlIEFwcCBBdHRlc3RhdGlvbiBSb290IENBMRMwEQYDVQQKDApBcHBsZSBJbmMuMRMwEQYDVQQIDApDYWxpZm9ybmlhMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAERTHhmLW07ATaFQIEVwTtT4dyctdhNbJhFs/Ii2FdCgAHGbpphY3+d8qjuDngIN3WVhQUBHAoMeQ/cLiP1sOUtgjqK9auYen1mMEvRq9Sk3Jm5X8U62H+xTD3FE9TgS41o0IwQDAPBgNVHRMBAf8EBTADAQH/MB0GA1UdDgQWBBSskRBTM72+aEH/pwyp5frq5eWKoTAOBgNVHQ8BAf8EBAMCAQYwCgYIKoZIzj0EAwMDaAAwZQIwQgFGnByvsiVbpTKwSga0kP0e8EeDS4+sQmTvb7vn53O5+FRXgeLhpJ06ysC5PrOyAjEAp5U4xDgEgllF7En3VcE3iexZZtKeYnpqtijVoyFraWVIyd/dganmrduC1bmTBGwD\n-----END CERTIFICATE-----")

export default {
  async fetch(request, env) {
    try {
      const url = new URL(request.url)
      if (request.method === "GET" && url.pathname === "/health") {
        const appAttestConfigured = Boolean(env.APP_ATTEST_CHALLENGES && env.APPLE_TEAM_ID && (env.APP_ATTEST_BUNDLE_ID || env.APNS_TOPIC))
        const apnsConfigured = Boolean(env.APNS_KEY_P8 && env.APNS_KEY_ID && env.APPLE_TEAM_ID && env.APNS_TOPIC)
        const rateLimitsConfigured = Boolean(env.REGISTER_RATE_LIMITER && env.SEND_RATE_LIMITER)
        const configured = appAttestConfigured && apnsConfigured && rateLimitsConfigured
        return json({ ok: configured, service: "agenthail-push", version: env.RELAY_VERSION || "dev", protocol: 2, capabilities: ["apns", "app-attest", "credential-auth", "expiring-registration", "rate-limit"], appAttest: { required: true, configured: appAttestConfigured }, apns: { configured: apnsConfigured }, rateLimits: { configured: rateLimitsConfigured } })
      }
      if (request.method === "POST" && url.pathname === "/v1/attest/challenge") {
        return await issueChallenge(request, env)
      }
      if (request.method === "POST" && url.pathname === "/v1/register") {
        return await register(request, env)
      }
      if (request.method === "POST" && url.pathname === "/v1/send") {
        return await send(request, env)
      }
      if (request.method === "DELETE" && url.pathname === "/v1/register") {
        return await revoke(request, env)
      }
      return json({ error: "not_found" }, 404)
    } catch {
      return json({ error: "push_service_unavailable" }, 503)
    }
  }
}

export async function register(request, env, verifier = verifyAppAttestation) {
  const clientAddress = clean(request.headers.get("cf-connecting-ip"), 64) || "unknown"
  if (!env.REGISTER_RATE_LIMITER) return json({ error: "rate_limiter_unavailable" }, 503)
  if (!await allowRequest(env.REGISTER_RATE_LIMITER, `register:${clientAddress}`)) {
    return json({ error: "rate_limited" }, 429)
  }
  const body = await readJSON(request)
  const token = clean(body?.deviceToken, 256)
  const environment = body?.environment === "sandbox" ? "sandbox" : "production"
  if (!/^[a-fA-F0-9]{64,256}$/.test(token)) {
    return json({ error: "invalid_device_token" }, 400)
  }
  if (!env.APP_ATTEST_CHALLENGES) return json({ error: "app_attest_store_unavailable" }, 503)
  const teamIdentifier = boundedString(env.APPLE_TEAM_ID, 32)
  const bundleIdentifier = boundedString(env.APP_ATTEST_BUNDLE_ID || env.APNS_TOPIC, 255)
  if (!teamIdentifier || !bundleIdentifier) return json({ error: "app_attest_configuration_unavailable" }, 503)
  const challengeId = boundedString(body?.challengeId, 80)
  const keyId = boundedString(body?.keyId, 128)
  const attestation = boundedString(body?.attestation, 7800)
  if (!isUUID(challengeId) || !isBase64(keyId, 32) || !isBase64(attestation, 1, 5800)) {
    return json({ error: "invalid_app_attestation" }, 400)
  }
  const challengeStub = env.APP_ATTEST_CHALLENGES.getByName(challengeId)
  const challengeResponse = await challengeStub.fetch("https://challenge.internal/read", { method: "POST" })
  if (!challengeResponse.ok) return challengeError(challengeResponse)
  const challengeState = await challengeResponse.json()
  try {
    await verifier({
      attestation,
      challenge: challengeState.challenge,
      keyId,
      bundleIdentifier,
      teamIdentifier,
      allowDevelopmentEnvironment: env.APP_ATTEST_ALLOW_DEVELOPMENT === "true"
    })
  } catch {
    return json({ error: "app_attestation_rejected" }, 401)
  }
  const consumed = await challengeStub.fetch("https://challenge.internal/consume", { method: "POST" })
  if (!consumed.ok) return challengeError(consumed)
  const id = crypto.randomUUID()
  const credential = randomSecret(32)
  const expiresAt = Date.now() + registrationTTLSeconds * 1000
  await env.PUSH_DEVICES.put(`device:${id}`, JSON.stringify({
    token: token.toLowerCase(),
    environment,
    credentialHash: await sha256(credential),
    appAttestKeyHash: await sha256(keyId),
    attestedAt: new Date().toISOString(),
    createdAt: new Date().toISOString()
  }), { expirationTtl: registrationTTLSeconds })
  return json({ installationId: id, credential, expiresAt }, 201)
}

export async function issueChallenge(request, env) {
  const clientAddress = clean(request.headers.get("cf-connecting-ip"), 64) || "unknown"
  if (!env.REGISTER_RATE_LIMITER) return json({ error: "rate_limiter_unavailable" }, 503)
  if (!await allowRequest(env.REGISTER_RATE_LIMITER, `attest:${clientAddress}`)) {
    return json({ error: "rate_limited" }, 429)
  }
  if (!env.APP_ATTEST_CHALLENGES) return json({ error: "app_attest_store_unavailable" }, 503)
  if (!boundedString(env.APPLE_TEAM_ID, 32) || !boundedString(env.APP_ATTEST_BUNDLE_ID || env.APNS_TOPIC, 255)) {
    return json({ error: "app_attest_configuration_unavailable" }, 503)
  }
  const challengeId = crypto.randomUUID()
  const challenge = randomSecret(32)
  const expiresAt = Date.now() + challengeTTLMilliseconds
  const stub = env.APP_ATTEST_CHALLENGES.getByName(challengeId)
  const stored = await stub.fetch("https://challenge.internal/issue", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ challenge, expiresAt })
  })
  if (!stored.ok) return json({ error: "app_attest_store_unavailable" }, 503)
  return json({ challengeId, challenge, expiresAt }, 201)
}

export class AppAttestChallenge {
  constructor(ctx) {
    this.ctx = ctx
  }

  async fetch(request) {
    const pathname = new URL(request.url).pathname
    if (request.method !== "POST") return json({ error: "method_not_allowed" }, 405)
    if (pathname === "/issue") {
      const body = await readJSON(request)
      const challenge = boundedString(body?.challenge, 64)
      const expiresAt = Number(body?.expiresAt)
      if (!isBase64URL(challenge, 32) || !Number.isSafeInteger(expiresAt) || expiresAt <= Date.now()) {
        return json({ error: "invalid_challenge" }, 400)
      }
      const existing = await this.ctx.storage.get("challenge")
      if (existing) return json({ error: "challenge_exists" }, 409)
      await this.ctx.storage.put("challenge", { challenge, expiresAt, consumed: false })
      await this.ctx.storage.setAlarm(expiresAt)
      return json({ ok: true }, 201)
    }
    if (pathname === "/consume") {
      const outcome = await this.ctx.storage.transaction(async transaction => {
        const state = await transaction.get("challenge")
        if (!state) return { error: "challenge_not_found", status: 404 }
        if (state.expiresAt <= Date.now()) {
          await transaction.deleteAll()
          return { error: "challenge_expired", status: 410 }
        }
        if (state.consumed) return { error: "challenge_replayed", status: 409 }
        await transaction.put("challenge", { ...state, consumed: true })
        return { ok: true, status: 200 }
      })
      const { status, ...body } = outcome
      return json(body, status)
    }
    const state = await this.ctx.storage.get("challenge")
    if (!state) return json({ error: "challenge_not_found" }, 404)
    if (state.expiresAt <= Date.now()) {
      await this.ctx.storage.deleteAll()
      return json({ error: "challenge_expired" }, 410)
    }
    if (state.consumed) return json({ error: "challenge_replayed" }, 409)
    if (pathname === "/read") return json({ challenge: state.challenge, expiresAt: state.expiresAt })
    return json({ error: "not_found" }, 404)
  }

  async alarm() {
    await this.ctx.storage.deleteAll()
  }
}

export async function send(request, env) {
  const body = await readJSON(request)
  const id = clean(body?.installationId, 160)
  const credential = clean(body?.credential, 256)
  if (!id || !credential) return json({ error: "unauthorized" }, 401)
  const key = `device:${id}`
  const stored = await env.PUSH_DEVICES.get(key, "json")
  if (!stored || !stored.attestedAt || !stored.appAttestKeyHash || !timingSafeEqual(stored.credentialHash, await sha256(credential))) {
    if (stored) await env.PUSH_DEVICES.delete(key)
    return json({ error: "unauthorized" }, 401)
  }
  if (!env.SEND_RATE_LIMITER) return json({ error: "rate_limiter_unavailable" }, 503)
  if (!await allowRequest(env.SEND_RATE_LIMITER, `send:${id}`)) {
    return json({ error: "rate_limited" }, 429)
  }
  const title = clean(body?.title, 120)
  const message = clean(body?.message, 1200)
  if (!title || !message) return json({ error: "invalid_notification" }, 400)
  const host = stored.environment === "sandbox" ? "api.sandbox.push.apple.com" : "api.push.apple.com"
  const providerToken = await apnsProviderToken(env)
  const payload = {
    aps: {
      alert: { title, body: message },
      sound: "default",
      "thread-id": "agenthail"
    },
    sessionId: clean(body?.sessionId, 240),
    eventType: clean(body?.eventType, 80)
  }
  const response = await fetch(`https://${host}/3/device/${stored.token}`, {
    method: "POST",
    headers: {
      authorization: `bearer ${providerToken}`,
      "apns-topic": env.APNS_TOPIC,
      "apns-push-type": "alert",
      "apns-priority": "10",
      "content-type": "application/json"
    },
    body: JSON.stringify(payload)
  })
  if (response.ok) {
    await env.PUSH_DEVICES.put(key, JSON.stringify(stored), { expirationTtl: registrationTTLSeconds })
    return json({ ok: true })
  }
  const reason = await response.text()
  if (response.status === 410 || reason.includes("BadDeviceToken") || reason.includes("Unregistered")) {
    await env.PUSH_DEVICES.delete(key)
  }
  return json({ error: "apns_rejected", status: response.status, reason: clean(reason, 300) }, 502)
}

export async function revoke(request, env) {
  const body = await readJSON(request)
  const id = clean(body?.installationId, 160)
  const credential = clean(body?.credential, 256)
  const key = `device:${id}`
  const stored = id ? await env.PUSH_DEVICES.get(key, "json") : null
  if (!stored || !timingSafeEqual(stored.credentialHash, await sha256(credential))) {
    return json({ error: "unauthorized" }, 401)
  }
  await env.PUSH_DEVICES.delete(key)
  return json({ ok: true })
}

async function apnsProviderToken(env) {
  const now = Math.floor(Date.now() / 1000)
  if (cachedProviderToken && now - cachedProviderToken.createdAt < 3000) return cachedProviderToken.value
  if (!env.APNS_KEY_P8 || !env.APNS_KEY_ID || !env.APPLE_TEAM_ID || !env.APNS_TOPIC) {
    throw new Error("APNs provider configuration is incomplete")
  }
  const header = base64url(encoder.encode(JSON.stringify({ alg: "ES256", kid: env.APNS_KEY_ID })))
  const claims = base64url(encoder.encode(JSON.stringify({ iss: env.APPLE_TEAM_ID, iat: now })))
  const signingInput = `${header}.${claims}`
  const keyData = pemBytes(env.APNS_KEY_P8)
  const key = await crypto.subtle.importKey("pkcs8", keyData, { name: "ECDSA", namedCurve: "P-256" }, false, ["sign"])
  const signature = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, key, encoder.encode(signingInput))
  const value = `${signingInput}.${base64url(new Uint8Array(signature))}`
  cachedProviderToken = { createdAt: now, value }
  return value
}

async function readJSON(request) {
  if (!request.headers.get("content-type")?.toLowerCase().startsWith("application/json")) return null
  const lengthHeader = request.headers.get("content-length")
  if (lengthHeader && (!/^\d+$/.test(lengthHeader) || Number(lengthHeader) > 8192)) return null
  if (!request.body) return null
  const reader = request.body.getReader()
  const chunks = []
  let size = 0
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      size += value.byteLength
      if (size > 8192) {
        await reader.cancel()
        return null
      }
      chunks.push(value)
    }
    const bytes = new Uint8Array(size)
    let offset = 0
    for (const chunk of chunks) {
      bytes.set(chunk, offset)
      offset += chunk.byteLength
    }
    return JSON.parse(decoder.decode(bytes))
  } catch {
    await reader.cancel().catch(() => {})
    return null
  }
}

async function allowRequest(limiter, key) {
  const result = await limiter.limit({ key })
  return result.success
}

function clean(value, limit) {
  if (typeof value !== "string") return ""
  return value.trim().slice(0, limit)
}

function boundedString(value, limit) {
  if (typeof value !== "string") return ""
  const trimmed = value.trim()
  return trimmed.length <= limit ? trimmed : ""
}

function isUUID(value) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value)
}

function isBase64(value, minimumBytes, maximumBytes = minimumBytes) {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) return false
  const bytes = Buffer.from(value, "base64")
  return bytes.length >= minimumBytes && bytes.length <= maximumBytes && bytes.toString("base64") === value
}

function isBase64URL(value, expectedBytes) {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) return false
  return decodeBase64URL(value)?.length === expectedBytes
}

function decodeBase64URL(value) {
  try {
    const padding = "=".repeat((4 - value.length % 4) % 4)
    return Buffer.from(value.replace(/-/g, "+").replace(/_/g, "/") + padding, "base64")
  } catch {
    return null
  }
}

export async function verifyAppAttestation({ attestation, challenge, keyId, bundleIdentifier, teamIdentifier, allowDevelopmentEnvironment, now = Date.now() }) {
  const attestationBytes = Buffer.from(attestation, "base64")
  const challengeBytes = decodeBase64URL(challenge)
  if (!challengeBytes) throw new Error("invalid challenge")
  const decodedItems = cbor.decodeAllSync(attestationBytes)
  if (decodedItems.length !== 1) throw new Error("invalid attestation")
  const decoded = decodedItems[0]
  if (decoded?.fmt !== "apple-appattest" || !Array.isArray(decoded?.attStmt?.x5c) || decoded.attStmt.x5c.length !== 2 || !Buffer.isBuffer(decoded.attStmt.receipt)) {
    throw new Error("invalid attestation")
  }
  const authData = decoded.authData
  if (!Buffer.isBuffer(authData) || authData.length < 87 || (authData[32] & 0x40) === 0) throw new Error("invalid authenticator data")
  const leaf = new X509Certificate(decoded.attStmt.x5c[0])
  const intermediate = new X509Certificate(decoded.attStmt.x5c[1])
  if (!leaf.verify(intermediate.publicKey) || !intermediate.verify(appAttestRoot.publicKey) || leaf.ca || !intermediate.ca) {
    throw new Error("invalid certificate chain")
  }
  for (const certificate of [leaf, intermediate]) {
    if (Date.parse(certificate.validFrom) > now || Date.parse(certificate.validTo) < now) {
      throw new Error("certificate outside validity period")
    }
  }
  const clientDataHash = digest(challengeBytes)
  const nonce = digest(Buffer.concat([authData, clientDataHash]))
  const schema = asn1js.fromBER(leaf.raw)
  if (schema.offset === -1) throw new Error("invalid certificate")
  const parsedCertificate = new pkijs.Certificate({ schema: schema.result })
  const nonceExtension = parsedCertificate.extensions?.find(extension => extension.extnID === appAttestNonceOID)
  const actualNonce = nonceExtension?.parsedValue?.valueBlock?.value?.[0]?.valueBlock?.value?.[0]?.valueBlock?.valueHexView
  if (!actualNonce || !timingSafeBytes(Buffer.from(actualNonce), nonce)) throw new Error("nonce mismatch")
  const publicKey = Buffer.from(parsedCertificate.subjectPublicKeyInfo.subjectPublicKey.valueBlock.valueHexView)
  if (!timingSafeEqual(digest(publicKey).toString("base64"), keyId)) throw new Error("key identifier mismatch")
  const expectedRPID = digest(Buffer.from(`${teamIdentifier}.${bundleIdentifier}`))
  if (!timingSafeBytes(authData.subarray(0, 32), expectedRPID)) throw new Error("app identifier mismatch")
  if (authData.readUInt32BE(33) !== 0) throw new Error("invalid counter")
  const productionAAGUID = Buffer.concat([Buffer.from("appattest"), Buffer.alloc(7)])
  const developmentAAGUID = Buffer.from("appattestdevelop")
  const aaguid = authData.subarray(37, 53)
  if (!aaguid.equals(productionAAGUID) && !(allowDevelopmentEnvironment && aaguid.equals(developmentAAGUID))) {
    throw new Error("invalid attestation environment")
  }
  const credentialLength = authData.readUInt16BE(53)
  if (credentialLength !== 32 || !timingSafeBytes(authData.subarray(55, 87), Buffer.from(keyId, "base64"))) {
    throw new Error("credential identifier mismatch")
  }
  return { publicKeyPem: leaf.publicKey.export({ type: "spki", format: "pem" }), receipt: decoded.attStmt.receipt }
}

async function challengeError(response) {
  const body = await response.json().catch(() => ({}))
  const error = typeof body.error === "string" ? body.error : "app_attest_store_unavailable"
  const status = error === "challenge_expired" ? 410 : error === "challenge_replayed" ? 409 : error === "challenge_not_found" ? 404 : 503
  return json({ error }, status)
}

function randomSecret(size) {
  const bytes = new Uint8Array(size)
  crypto.getRandomValues(bytes)
  return base64url(bytes)
}

async function sha256(value) {
  return base64url(new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(value))))
}

function timingSafeEqual(left, right) {
  if (typeof left !== "string" || left.length !== right.length) return false
  let difference = 0
  for (let index = 0; index < left.length; index += 1) difference |= left.charCodeAt(index) ^ right.charCodeAt(index)
  return difference === 0
}

function timingSafeBytes(left, right) {
  if (!Buffer.isBuffer(left) || !Buffer.isBuffer(right) || left.length !== right.length) return false
  let difference = 0
  for (let index = 0; index < left.length; index += 1) difference |= left[index] ^ right[index]
  return difference === 0
}

function digest(value) {
  return createHash("sha256").update(value).digest()
}

function pemBytes(value) {
  const raw = value.replace(/-----BEGIN PRIVATE KEY-----|-----END PRIVATE KEY-----|\s/g, "")
  return Uint8Array.from(atob(raw), character => character.charCodeAt(0))
}

function base64url(bytes) {
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}

function json(value, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json", "cache-control": "no-store" } })
}
