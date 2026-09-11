import assert from "node:assert/strict"
import { generateKeyPairSync } from "node:crypto"
import test from "node:test"
import { createTestFlightManager } from "../scripts/manage-testflight.mjs"

const cfg = { issuerId: "issuer", keyId: "key", privateKey: generateKeyPairSync("ec", { namedCurve: "P-256" }).privateKey.export({ type: "pkcs8", format: "pem" }), bundleId: "com.example.app", groupId: "group-1", baseURL: "https://asc.test" }
const response = (status, body) => ({ ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body) })
function harness(routes) {
  const requests = []
  return { requests, fetchImpl: async (url, init) => { const key = `${init.method || "GET"} ${new URL(url).pathname}${new URL(url).search}`; requests.push(key); const reply = routes[key]; if (!reply) throw new Error(`unexpected ${key}`); return typeof reply === "function" ? reply(init) : reply } }
}
const app = { data: [{ id: "app-1", attributes: { bundleId: cfg.bundleId } }] }
const group = { data: { id: "group-1", attributes: { name: "Internal", isInternalGroup: true, hasAccessToAllBuilds: false } } }
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
  const h = harness(common({ "GET /v1/betaTesters?filter%5Bemail%5D=a%40example.com&limit=2": response(200, { data: [{ id: "a" }, { id: "b" }] }) }))
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
    "GET /v1/betaTesters?filter%5Bemail%5D=a%40example.com&limit=2": response(200, { data: [{ id: "tester-1", attributes: { inviteType: "EMAIL" } }] }),
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
