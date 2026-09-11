import assert from "node:assert/strict"
import { generateKeyPairSync } from "node:crypto"
import test from "node:test"
import { createTestFlightManager, parseArgs, runCLI } from "../scripts/manage-testflight.mjs"

const cfg = { issuerId: "issuer", keyId: "key", privateKey: generateKeyPairSync("ec", { namedCurve: "P-256" }).privateKey.export({ type: "pkcs8", format: "pem" }), bundleId: "com.example.app", groupId: "group-1", baseURL: "https://asc.test" }
const response = (status, body) => ({ ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body) })
function harness(routes) {
  const requests = []
  return { requests, fetchImpl: async (url, init) => { const key = `${init.method || "GET"} ${new URL(url).pathname}${new URL(url).search}`; requests.push(key); const reply = routes[key]; if (!reply) throw new Error(`unexpected ${key}`); return typeof reply === "function" ? reply(init) : reply } }
}
const app = { data: [{ id: "app-1", attributes: { bundleId: cfg.bundleId } }] }
const group = { data: { id: "group-1", attributes: { name: "Internal", isInternalGroup: true, hasAccessToAllBuilds: false } } }
function exactBuildRoutes(extra = {}) { return { "GET /v1/builds?filter%5Bapp%5D=app-1&filter%5Bversion%5D=7&filter%5BpreReleaseVersion.platform%5D=IOS&filter%5BpreReleaseVersion.version%5D=1.2.3&limit=200": response(200, { data: [{ id: "build-1", attributes: { version: "7", expired: false } }] }), ...extra } }
function common(extra = {}) { return { "GET /v1/apps?filter%5BbundleId%5D=com.example.app&limit=2": response(200, app), "GET /v1/betaGroups/group-1": response(200, group), "GET /v1/betaGroups/group-1/relationships/app": response(200, { data: { type: "apps", id: "app-1" } }), "GET /v1/betaGroups/group-1/relationships/builds": response(200, { data: [{ type: "builds", id: "build-1" }] }), "GET /v1/betaGroups/group-1/relationships/betaTesters": response(200, { data: [{ type: "betaTesters", id: "tester-1" }] }), "GET /v1/builds/build-1/buildBetaDetail": response(200, { data: { attributes: { internalBuildState: "READY_FOR_BETA_TESTING", externalBuildState: "" } } }), "GET /v1/betaTesters/tester-1": response(200, { data: { attributes: { state: "ACCEPTED" } } }), ...extra } }

test("status reports exact group readiness and counts without tester emails", async () => {
  const h = harness(common())
  const result = await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).status()
  assert.deepEqual(result.counts, { builds: 1, testers: 1 }); assert.equal(result.groupReady, true); assert.equal(result.invitationStates.ACCEPTED, 1); assert.equal(JSON.stringify(result).includes("@"), false)
})

test("status refuses unknown app group relationship", async () => {
  const h = harness(common({ "GET /v1/betaGroups/group-1/relationships/app": response(200, { data: [{ id: "other-app" }] }) }))
  const result = await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).status(); assert.equal(result.groupReady, false); assert.equal(result.appRelationship.exact, false)
})

test("pagination refuses a next URL outside the API origin", async () => {
  const h = harness({ "GET /v1/apps?filter%5BbundleId%5D=com.example.app&limit=2": response(200, { ...app, links: { next: "https://evil.test/v1/apps" } }) })
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).status, /escaped the API origin/)
})

test("ambiguous tester and forbidden API errors produce bounded failures", async () => {
  const h = harness(common({ "GET /v1/betaTesters?filter%5Bemail%5D=a%40example.com&limit=200": response(200, { data: [{ id: "a" }, { id: "b" }] }), "GET /v1/betaTesters/a/relationships/apps": response(200, { data: [{ id: "app-1" }] }), "GET /v1/betaTesters/b/relationships/apps": response(200, { data: [{ id: "app-1" }] }) }))
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).inviteExistingTester("a@example.com"), /expected one existing beta tester/)
  const forbidden = harness({ "GET /v1/apps?filter%5BbundleId%5D=com.example.app&limit=2": response(403, { errors: [{ code: "FORBIDDEN" }] }) })
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: forbidden.fetchImpl }).status, error => error.status === 403 && /FORBIDDEN/.test(error.message))
})

test("already assigned build is idempotent and does not POST", async () => {
  const h = harness(common({ "GET /v1/builds?filter%5Bapp%5D=app-1&filter%5Bversion%5D=7&filter%5BpreReleaseVersion.platform%5D=IOS&filter%5BpreReleaseVersion.version%5D=1.2.3&limit=200": response(200, { data: [{ id: "build-1", attributes: { version: "7" } }] }), "GET /v1/betaGroups/group-1/relationships/builds": response(200, { data: [{ id: "build-1" }] }) }))
  const result = await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).assignBuild("1.2.3", "7"); assert.equal(result.already, true); assert.equal(h.requests.some(item => item.startsWith("POST ")), false)
})

test("existing tester can be removed with exact email and readback", async () => {
  let assigned = true
  const h = harness(common({
    "GET /v1/betaGroups/group-1/betaTesters?limit=200": response(200, { data: [{ id: "tester-1", attributes: { inviteType: "EMAIL", email: "a@example.com" } }] }),
    "GET /v1/betaTesters/tester-1/relationships/apps": response(200, { data: [{ id: "app-1" }] }),
    "GET /v1/betaGroups/group-1/relationships/betaTesters": () => response(200, { data: assigned ? [{ id: "tester-1" }] : [] }),
    "DELETE /v1/betaGroups/group-1/relationships/betaTesters": () => { assigned = false; return response(204, {}) },
  }))
  await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).removeTester("a@example.com")
  assert.equal(h.requests.some(item => item.startsWith("DELETE ")), true)
})

test("wrong group membership blocks build mutation before POST", async () => {
  const h = harness(common({ "GET /v1/builds?filter%5Bapp%5D=app-1&filter%5Bversion%5D=7&filter%5BpreReleaseVersion.platform%5D=IOS&filter%5BpreReleaseVersion.version%5D=1.2.3&limit=200": response(200, { data: [{ id: "build-1", attributes: { version: "7" } }] }), "GET /v1/betaGroups/group-1/relationships/app": response(200, { data: [{ id: "wrong" }] }) }))
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).assignBuild("1.2.3", "7"), /not an exact internal group/); assert.equal(h.requests.some(item => item.startsWith("POST ")), false)
})

test("notes creates a localization and verifies the exact text", async () => {
  const h = harness({ ...common(), ...exactBuildRoutes({
    "GET /v1/betaBuildLocalizations?filter%5Bbuild%5D=build-1&filter%5Blocale%5D=en-US&limit=2": response(200, { data: [] }),
    "POST /v1/betaBuildLocalizations": response(201, { data: { id: "loc-1" } }),
    "GET /v1/betaBuildLocalizations/loc-1": response(200, { data: { attributes: { whatsNew: "Try the new relay" } } })
  }) })
  const result = await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).notes("1.2.3", "7", "en-US", "Try the new relay")
  assert.equal(result.changed, true); assert.equal(h.requests.some(item => item.startsWith("POST /v1/betaBuildLocalizations")), true)
})

test("notes updates an existing localization and fails on readback mismatch", async () => {
  const h = harness({ ...common(), ...exactBuildRoutes({
    "GET /v1/betaBuildLocalizations?filter%5Bbuild%5D=build-1&filter%5Blocale%5D=en-US&limit=2": response(200, { data: [{ id: "loc-1" }] }),
    "PATCH /v1/betaBuildLocalizations/loc-1": response(200, {}),
    "GET /v1/betaBuildLocalizations/loc-1": response(200, { data: { attributes: { whatsNew: "old text" } } })
  }) })
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).notes("1.2.3", "7", "en-US", "new text"), /readback did not confirm/)
  assert.equal(h.requests.some(item => item.startsWith("PATCH /v1/betaBuildLocalizations")), true)
})

test("expire-build targets the exact build, is idempotent, and requires selectors", async () => {
  let expired = false
  const h = harness({ ...common(), ...exactBuildRoutes({
    "GET /v1/builds?filter%5Bapp%5D=app-1&filter%5Bversion%5D=7&filter%5BpreReleaseVersion.platform%5D=IOS&filter%5BpreReleaseVersion.version%5D=1.2.3&limit=200": () => response(200, { data: [{ id: "build-1", attributes: { version: "7", expired } }] }),
    "PATCH /v1/builds/build-1": () => { expired = true; return response(200, {}) },
    "GET /v1/builds/build-1": () => response(200, { data: { attributes: { expired } } })
  }) })
  const manager = createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl })
  assert.equal((await manager.expireBuild("1.2.3", "7")).changed, true)
  assert.equal((await manager.expireBuild("1.2.3", "7")).already, true)
  assert.equal(h.requests.filter(item => item.startsWith("PATCH ")).length, 1)
  await assert.rejects(manager.expireBuild(undefined, "7"), /missing version/)
  await assert.rejects(manager.expireBuild("1.2.3", undefined), /missing build/)
})

test("CLI status accepts omitted optional version and build inputs", async () => {
  const h = harness(common())
  const output = { value: "", write(value) { this.value += value } }
  const env = { APPLE_NOTARY_KEY_BASE64: Buffer.from(cfg.privateKey).toString("base64"), APPLE_NOTARY_KEY_ID: cfg.keyId, APPLE_NOTARY_ISSUER_ID: cfg.issuerId, AGENTHAIL_IOS_BUNDLE_ID: cfg.bundleId, AGENTHAIL_TESTFLIGHT_GROUP_ID: cfg.groupId }
  const result = await runCLI({ argv: ["status"], env, fetchImpl: h.fetchImpl, stdout: output })
  assert.equal(result.groupReady, true); assert.equal(result.buildCheck, undefined); assert.equal(output.value.endsWith("\n"), true)
  assert.deepEqual(parseArgs(["status", "--version", "1.2.3", "--build", "7"]), { action: "status", version: "1.2.3", build: "7" })
})

test("resend-invitation posts exact app and existing tester relationships and reads state", async () => {
  let invitationBody
  const h = harness({ ...common({
    "GET /v1/betaGroups/group-1/betaTesters?limit=200": response(200, { data: [{ id: "tester-1", attributes: { email: "a@example.com" } }] }),
    "GET /v1/betaTesters/tester-1/relationships/apps": response(200, { data: [{ type: "apps", id: "app-1" }] }),
    "POST /v1/betaTesterInvitations": init => { invitationBody = JSON.parse(init.body); return response(201, { data: { id: "invitation-1" } }) },
    "GET /v1/betaTesters/tester-1": response(200, { data: { attributes: { state: "ACCEPTED" } } })
  }) })
  const result = await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).resendInvitation("a@example.com")
  assert.deepEqual(invitationBody, { data: { type: "betaTesterInvitations", relationships: { app: { data: { type: "apps", id: "app-1" } }, betaTester: { data: { type: "betaTesters", id: "tester-1" } } } } })
  assert.equal(result.requested, true); assert.equal(result.state, "ACCEPTED"); assert.equal(JSON.stringify(result).includes("a@example.com"), false)
})

test("resend-invitation does not retry a failed POST", async () => {
  let posts = 0
  const h = harness({ ...common({
    "GET /v1/betaGroups/group-1/betaTesters?limit=200": response(200, { data: [{ id: "tester-1", attributes: { email: "a@example.com" } }] }),
    "GET /v1/betaTesters/tester-1/relationships/apps": response(200, { data: [{ id: "app-1" }] }),
    "POST /v1/betaTesterInvitations": () => { posts += 1; return response(503, { errors: [{ code: "TEMPORARY" }] }) }
  }) })
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).resendInvitation("a@example.com"), /returned 503/)
  assert.equal(posts, 1)
})

test("resend-invitation rejects a group for another app before POST", async () => {
  let posts = 0
  const h = harness(common({
    "GET /v1/betaGroups/group-1/relationships/app": response(200, { data: [{ id: "other-app" }] }),
    "GET /v1/betaGroups/group-1/betaTesters?limit=200": response(200, { data: [{ id: "tester-1", attributes: { email: "a@example.com" } }] }),
    "POST /v1/betaTesterInvitations": () => { posts += 1; return response(201, { data: { id: "invitation-1" } }) }
  }))
  await assert.rejects(createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).resendInvitation("a@example.com"), /not an exact internal group/)
  assert.equal(posts, 0)
})

test("removing an absent group tester is idempotent but resending is rejected", async () => {
  const h = harness(common({
    "GET /v1/betaGroups/group-1/betaTesters?limit=200": response(200, { data: [] })
  }))
  const manager = createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl })
  assert.equal((await manager.removeTester("a@example.com")).already, true)
  await assert.rejects(manager.resendInvitation("a@example.com"), /not assigned/)
  assert.equal(h.requests.every(item => item.startsWith("GET ")), true)
})

test("an already accepted invitation is a successful no-op", async () => {
  let posts = 0
  const h = harness(common({
    "GET /v1/betaGroups/group-1/betaTesters?limit=200": response(200, { data: [{ id: "tester-1", attributes: { email: "a@example.com" } }] }),
    "GET /v1/betaTesters/tester-1/relationships/apps": response(200, { data: [{ id: "app-1" }] }),
    "POST /v1/betaTesterInvitations": () => { posts += 1; return response(409, { errors: [{ code: "STATE_ERROR.TESTER_INVITE.ALREADY_ACCEPTED" }] }) }
  }))
  const result = await createTestFlightManager({ ...cfg, fetchImpl: h.fetchImpl }).resendInvitation("a@example.com")
  assert.equal(result.requested, false)
  assert.equal(result.already, true)
  assert.equal(result.state, "ACCEPTED")
  assert.equal(posts, 1)
})

test("CLI status reports the snapshot after the readiness gate", async () => {
  let processed = false
  const build = { id: "build-1", attributes: { version: "7", processingState: "VALID", expired: false } }
  const fetchImpl = async input => {
    const url = new URL(input)
    switch (url.pathname) {
      case "/v1/apps": return response(200, app)
      case "/v1/builds":
        if (url.searchParams.has("fields[builds]")) processed = true
        return response(200, { data: [build] })
      case "/v1/betaGroups/group-1": return response(200, { data: { ...group.data, attributes: { ...group.data.attributes, hasAccessToAllBuilds: true } } })
      case "/v1/betaGroups/group-1/app":
      case "/v1/betaGroups/group-1/relationships/app":
      case "/v1/builds/build-1/app": return response(200, { data: { id: "app-1" } })
      case "/v1/betaGroups/group-1/relationships/builds": return response(200, { data: processed ? [build] : [] })
      case "/v1/betaGroups/group-1/betaTesters":
      case "/v1/betaGroups/group-1/relationships/betaTesters": return response(200, { data: [{ id: "tester-1" }] })
      case "/v1/builds/build-1/buildBetaDetail": return response(200, { data: { attributes: { internalBuildState: "IN_BETA_TESTING" } } })
      case "/v1/betaTesters/tester-1": return response(200, { data: { attributes: { state: "INSTALLED" } } })
      default: throw new Error(`unexpected path ${url.pathname}`)
    }
  }
  const output = { value: "", write(value) { this.value += value } }
  const env = { APPLE_NOTARY_KEY_BASE64: Buffer.from(cfg.privateKey).toString("base64"), APPLE_NOTARY_KEY_ID: cfg.keyId, APPLE_NOTARY_ISSUER_ID: cfg.issuerId, AGENTHAIL_IOS_BUNDLE_ID: cfg.bundleId, AGENTHAIL_TESTFLIGHT_GROUP_ID: cfg.groupId }
  const result = await runCLI({ argv: ["status", "--version", "1.2.3", "--build", "7"], env, fetchImpl, stdout: output })
  assert.equal(result.ready, true)
  assert.equal(result.buildCheck.assignedToGroup, true)
  assert.equal(result.buildCheck.state.internalBuildState, result.internalGate.internalBuildState)
  assert.equal(JSON.parse(output.value).counts.builds, 1)
})
