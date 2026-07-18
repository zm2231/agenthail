import assert from "node:assert/strict"
import { generateKeyPairSync } from "node:crypto"
import test from "node:test"
import { acceptedProbe, createProviderToken } from "../scripts/verify-apns-credentials.mjs"

test("provider token carries the configured key and team identifiers", async () => {
  const { privateKey } = generateKeyPairSync("ec", { namedCurve: "P-256" })
  const token = await createProviderToken({
    keyID: "KEY123",
    teamID: "TEAM123",
    privateKey: privateKey.export({ type: "pkcs8", format: "pem" }),
    now: 1234567890
  })
  const [header, claims, signature] = token.split(".")
  assert.deepEqual(JSON.parse(Buffer.from(header, "base64url")), { alg: "ES256", kid: "KEY123" })
  assert.deepEqual(JSON.parse(Buffer.from(claims, "base64url")), { iss: "TEAM123", iat: 1234567890 })
  assert.equal(Buffer.from(signature, "base64url").length, 64)
})

test("only Apple's authenticated dummy-token response passes the release gate", () => {
  assert.equal(acceptedProbe(400, "BadDeviceToken"), true)
  assert.equal(acceptedProbe(403, "InvalidProviderToken"), false)
  assert.equal(acceptedProbe(403, "ExpiredProviderToken"), false)
  assert.equal(acceptedProbe(403, "TopicDisallowed"), false)
  assert.equal(acceptedProbe(400, "BadTopic"), false)
})
