import { pathToFileURL } from "node:url"
import { createAppStoreToken, waitForInternalBuild } from "./wait-testflight-build.mjs"

const apiBase = "https://api.appstoreconnect.apple.com"
const pageLimit = 200
const maxPages = 20

export class TestFlightError extends Error {
  constructor(message, { status, code } = {}) {
    super(message)
    this.name = "TestFlightError"
    this.status = status
    this.code = code
  }
}

function required(value, name) {
  if (!value) throw new TestFlightError(`missing ${name}`)
  return value
}

function jsonHeaders(token) {
  return { authorization: `Bearer ${token}`, "content-type": "application/json" }
}

async function request(url, { token, fetchImpl, method = "GET", body } = {}) {
  let response
  try {
    response = await fetchImpl(url, {
      method,
      headers: jsonHeaders(token),
      ...(body === undefined ? {} : { body: JSON.stringify(body) })
    })
  } catch (error) {
    throw new TestFlightError(`App Store Connect request failed: ${error.message}`, { code: "network" })
  }
  const raw = await response.text()
  let document = {}
  if (raw) {
    try { document = JSON.parse(raw) } catch { throw new TestFlightError("App Store Connect returned invalid JSON", { status: response.status }) }
  }
  if (!response.ok) {
    const detail = document.errors?.[0]?.code || document.errors?.[0]?.status
    const parameter = document.errors?.[0]?.source?.parameter
    throw new TestFlightError(`App Store Connect ${method} ${new URL(url).pathname} returned ${response.status}${detail ? ` (${detail})` : ""}${parameter ? ` parameter=${parameter}` : ""}`, { status: response.status, code: detail })
  }
  return document
}

function safeNextURL(next, origin) {
  if (!next) return null
  const url = new URL(next)
  if (url.origin !== origin) throw new TestFlightError("App Store Connect pagination escaped the API origin")
  return url
}

async function list(url, options) {
  const origin = new URL(url).origin
  const data = []
  let next = new URL(url)
  const limit = options.maxPageCount || maxPages
  for (let page = 0; next && page < limit; page += 1) {
    const document = await request(next, options)
    if (Array.isArray(document.data)) data.push(...document.data)
    next = safeNextURL(document.links?.next, origin)
  }
  if (next) throw new TestFlightError(`App Store Connect pagination exceeded ${limit} pages`)
  return data
}

function linkageURL(baseURL, type, id, relationship) {
  return new URL(`/v1/${type}/${encodeURIComponent(id)}/relationships/${relationship}`, baseURL)
}

function linkageIds(document) {
  const entries = Array.isArray(document.data) ? document.data : document.data ? [document.data] : []
  return new Set(entries.map(item => item.id))
}

function buildQuery(baseURL, appId, version, build) {
  const url = new URL("/v1/builds", baseURL)
  url.searchParams.set("filter[app]", appId)
  url.searchParams.set("filter[version]", build)
  url.searchParams.set("filter[preReleaseVersion.platform]", "IOS")
  url.searchParams.set("filter[preReleaseVersion.version]", version.replace(/^v/, ""))
  url.searchParams.set("limit", String(pageLimit))
  return url
}

export function parseArgs(argv) {
  const [action, ...rest] = argv
  const args = { action }
  for (let i = 0; i < rest.length; i += 1) {
    const item = rest[i]
    if (!item.startsWith("--")) throw new TestFlightError(`unexpected argument ${item}`)
    const key = item.slice(2).replaceAll("-", "_")
    args[key] = rest[i + 1]?.startsWith("--") ? true : rest[++i]
  }
  return args
}

export function createTestFlightManager({
  issuerId, keyId, privateKey, bundleId, groupId, baseURL = apiBase,
  fetchImpl = fetch, now = Date.now, maxPageCount = maxPages
}) {
  required(issuerId, "APPLE_NOTARY_ISSUER_ID")
  required(keyId, "APPLE_NOTARY_KEY_ID")
  required(privateKey, "APPLE_NOTARY_KEY_BASE64 decoded key")
  required(bundleId, "AGENTHAIL_IOS_BUNDLE_ID")
  required(groupId, "AGENTHAIL_TESTFLIGHT_GROUP_ID")
  const root = new URL(baseURL)
  const options = () => ({ token: createAppStoreToken({ issuerId, keyId, privateKey, now: now() }), fetchImpl })
  const boundedList = (url) => list(url, { ...options(), maxPageCount })

  async function app() {
    const url = new URL("/v1/apps", root)
    url.searchParams.set("filter[bundleId]", bundleId)
    url.searchParams.set("limit", "2")
    const apps = await boundedList(url)
    if (apps.length !== 1) throw new TestFlightError(`expected one App Store app for ${bundleId}, found ${apps.length}`)
    return apps[0]
  }

  async function exactBuild(version, buildNumber) {
    required(version, "version")
    required(buildNumber, "build")
    const appResource = await app()
    const builds = await boundedList(buildQuery(root, appResource.id, version, buildNumber))
    const matches = builds.filter(item => item.attributes?.version === String(buildNumber))
    if (matches.length !== 1) throw new TestFlightError(`expected one exact TestFlight build ${version} (${buildNumber}), found ${matches.length}`)
    return { app: appResource, build: matches[0] }
  }

  async function groupState(appResource, includeDetails = false) {
    const group = await request(new URL(`/v1/betaGroups/${encodeURIComponent(groupId)}`, root), options())
    const groupApp = await request(linkageURL(root, "betaGroups", groupId, "app"), options())
    const builds = await boundedList(linkageURL(root, "betaGroups", groupId, "builds"))
    const testers = await boundedList(linkageURL(root, "betaGroups", groupId, "betaTesters"))
    const appIds = linkageIds(groupApp)
    const exactApp = appIds.size === 1 && appIds.has(appResource.id)
    const internal = group.data?.attributes?.isInternalGroup === true
    return {
      group: { id: groupId, name: group.data?.attributes?.name || null, isInternalGroup: internal },
      ready: exactApp && internal,
      appRelationship: { exact: exactApp, count: appIds.size },
      autodistribution: { enabled: group.data?.attributes?.hasAccessToAllBuilds === true },
      counts: { builds: builds.length, testers: testers.length },
      buildIds: new Set(builds.map(item => item.id)), testerIds: new Set(testers.map(item => item.id)),
      ...(includeDetails ? {
        buildStates: await Promise.all(builds.map(async build => {
          const detail = await request(new URL(`/v1/builds/${encodeURIComponent(build.id)}/buildBetaDetail`, root), options())
          return { buildId: build.id, internalBuildState: detail.data?.attributes?.internalBuildState || "UNKNOWN", externalBuildState: detail.data?.attributes?.externalBuildState || "UNKNOWN" }
        })),
        invitationStates: (await Promise.all(testers.map(async tester => {
          const detail = await request(new URL(`/v1/betaTesters/${encodeURIComponent(tester.id)}`, root), options())
          return detail.data?.attributes?.state || "UNKNOWN"
        }))).reduce((counts, state) => ({ ...counts, [state]: (counts[state] || 0) + 1 }), {})
      } : {})
    }
  }

  async function status(version, buildNumber) {
    const appResource = await app()
    const state = await groupState(appResource, true)
    const result = { app: { id: appResource.id, bundleId }, group: state.group, groupReady: state.ready, appRelationship: state.appRelationship, autodistribution: state.autodistribution, counts: state.counts, buildStates: state.buildStates, invitationStates: state.invitationStates }
    if (version || buildNumber) {
      if (!version || !buildNumber) throw new TestFlightError("status build check requires both version and build")
      const exact = await exactBuild(version, buildNumber)
      result.buildCheck = { version, build: String(buildNumber), buildId: exact.build.id, assignedToGroup: state.buildIds.has(exact.build.id), state: state.buildStates.find(item => item.buildId === exact.build.id) || null }
      result.ready = result.groupReady && result.buildCheck.assignedToGroup && ["READY_FOR_BETA_TESTING", "IN_BETA_TESTING"].includes(result.buildCheck.state?.internalBuildState)
    }
    return result
  }

  async function verifyMembership(relationship, id) {
    const entries = await boundedList(linkageURL(root, "betaGroups", groupId, relationship))
    return entries.some(item => item.id === id)
  }

  async function mutateLink({ relationship, resourceType, resourceId, add, label }) {
    const before = await verifyMembership(relationship, resourceId)
    if (before === add) return { action: label, changed: false, already: true, resourceId }
    const method = add ? "POST" : "DELETE"
    await request(linkageURL(root, "betaGroups", groupId, relationship), {
      ...options(), method,
      ...(add ? { body: { data: [{ type: resourceType, id: resourceId }] } } : { body: { data: [{ type: resourceType, id: resourceId }] } })
    })
    const after = await verifyMembership(relationship, resourceId)
    if (after !== add) throw new TestFlightError(`${label} readback did not confirm the requested state`)
    return { action: label, changed: true, resourceId }
  }

  async function assignBuild(version, buildNumber, add) {
    const { app: appResource, build } = await exactBuild(version, buildNumber)
    const state = await groupState(appResource)
    if (!state.ready) throw new TestFlightError("configured TestFlight group is not an exact internal group for this app")
    return mutateLink({ relationship: "builds", resourceType: "builds", resourceId: build.id, add, label: add ? "assign-build" : "remove-build" })
  }

  async function findTester(email, appId, inGroup = false) {
    const url = new URL("/v1/betaTesters", root)
    url.searchParams.set("filter[email]", email)
    url.searchParams.set("filter[apps]", appId)
    if (inGroup) url.searchParams.set("filter[betaGroups]", groupId)
    url.searchParams.set("limit", "2")
    const testers = await boundedList(url)
    if (inGroup && testers.length === 0) return null
    if (testers.length !== 1) throw new TestFlightError(`expected one existing beta tester for ${email}, found ${testers.length}`)
    const apps = await request(linkageURL(root, "betaTesters", testers[0].id, "apps"), options())
    if (!linkageIds(apps).has(appId)) throw new TestFlightError(`existing tester ${email} is not assigned to this app`)
    return testers[0]
  }

  async function inviteExistingTester(email) {
    required(email, "email")
    const appResource = await app()
    if (!(await groupState(appResource)).ready) throw new TestFlightError("configured TestFlight group is not an exact internal group for this app")
    const tester = await findTester(email, appResource.id)
    if (tester.attributes?.inviteType === "PUBLIC_LINK") throw new TestFlightError("public-link testers are outside this manager")
    return mutateLink({ relationship: "betaTesters", resourceType: "betaTesters", resourceId: tester.id, add: true, label: "invite-existing-tester" })
  }

  async function removeTester(email) {
    required(email, "email")
    const appResource = await app()
    if (!(await groupState(appResource)).ready) throw new TestFlightError("configured TestFlight group is not an exact internal group for this app")
    const tester = await findTester(email, appResource.id, true)
    if (!tester) return { action: "remove-tester", changed: false, already: true }
    return mutateLink({ relationship: "betaTesters", resourceType: "betaTesters", resourceId: tester.id, add: false, label: "remove-tester" })
  }

  async function resendInvitation(email) {
    required(email, "email")
    const appResource = await app()
    if (!(await groupState(appResource)).ready) throw new TestFlightError("configured TestFlight group is not an exact internal group for this app")
    const tester = await findTester(email, appResource.id, true)
    if (!tester) throw new TestFlightError("tester is not assigned to the configured group")
    if (!(await verifyMembership("betaTesters", tester.id))) throw new TestFlightError("tester is not assigned to the configured group")
    await request(new URL("/v1/betaTesterInvitations", root), {
      ...options(), method: "POST", body: { data: { type: "betaTesterInvitations", relationships: {
        app: { data: { type: "apps", id: appResource.id } },
        betaTester: { data: { type: "betaTesters", id: tester.id } }
      } } }
    })
    const readback = await request(new URL(`/v1/betaTesters/${encodeURIComponent(tester.id)}`, root), options())
    return { action: "resend-invitation", requested: true, testerId: tester.id, state: readback.data?.attributes?.state || "UNKNOWN" }
  }

  async function notes(version, buildNumber, locale, whatsNew) {
    required(locale, "locale")
    required(whatsNew, "notes")
    const { build } = await exactBuild(version, buildNumber)
    const url = new URL("/v1/betaBuildLocalizations", root)
    url.searchParams.set("filter[build]", build.id)
    url.searchParams.set("filter[locale]", locale)
    url.searchParams.set("limit", "2")
    const existing = await boundedList(url)
    if (existing.length > 1) throw new TestFlightError(`ambiguous beta build localization for ${locale}`)
    let id = existing[0]?.id
    if (id) {
      await request(new URL(`/v1/betaBuildLocalizations/${encodeURIComponent(id)}`, root), { ...options(), method: "PATCH", body: { data: { type: "betaBuildLocalizations", id, attributes: { whatsNew } } } })
    } else {
      const created = await request(new URL("/v1/betaBuildLocalizations", root), { ...options(), method: "POST", body: { data: { type: "betaBuildLocalizations", attributes: { locale, whatsNew }, relationships: { build: { data: { type: "builds", id: build.id } } } } } })
      id = created.data?.id
    }
    const readback = await request(new URL(`/v1/betaBuildLocalizations/${encodeURIComponent(id)}`, root), options())
    if (readback.data?.attributes?.whatsNew !== whatsNew) throw new TestFlightError("notes readback did not confirm the requested text")
    return { action: "notes", changed: true, buildId: build.id, locale }
  }

  async function buildPatch(version, buildNumber, attributes, action) {
    const { build } = await exactBuild(version, buildNumber)
    if (Object.entries(attributes).every(([key, value]) => build.attributes?.[key] === value)) return { action, changed: false, already: true, buildId: build.id }
    await request(new URL(`/v1/builds/${encodeURIComponent(build.id)}`, root), { ...options(), method: "PATCH", body: { data: { type: "builds", id: build.id, attributes } } })
    const readback = await request(new URL(`/v1/builds/${encodeURIComponent(build.id)}`, root), options())
    if (!Object.entries(attributes).every(([key, value]) => readback.data?.attributes?.[key] === value)) throw new TestFlightError(`${action} readback did not confirm the requested state`)
    return { action, changed: true, buildId: build.id }
  }

  return { status, assignBuild: (v, b) => assignBuild(v, b, true), removeBuild: (v, b) => assignBuild(v, b, false), inviteExistingTester, removeTester, resendInvitation, notes, expireBuild: (v, b) => buildPatch(v, b, { expired: true }, "expire-build") }
}

export async function runCLI({ argv = process.argv.slice(2), env = process.env, fetchImpl = fetch, stdout = process.stdout, now = Date.now } = {}) {
  const args = parseArgs(argv)
  const key = Buffer.from(required(env.APPLE_NOTARY_KEY_BASE64, "APPLE_NOTARY_KEY_BASE64"), "base64").toString("utf8")
  const manager = createTestFlightManager({ issuerId: env.APPLE_NOTARY_ISSUER_ID, keyId: env.APPLE_NOTARY_KEY_ID, privateKey: key, bundleId: env.AGENTHAIL_IOS_BUNDLE_ID, groupId: env.AGENTHAIL_TESTFLIGHT_GROUP_ID, fetchImpl, now })
  let result
  if (args.action === "status") {
    result = await manager.status(args.version, args.build)
    if (args.version || args.build) {
      const gate = await waitForInternalBuild({ groupId: env.AGENTHAIL_TESTFLIGHT_GROUP_ID, issuerId: env.APPLE_NOTARY_ISSUER_ID, keyId: env.APPLE_NOTARY_KEY_ID, privateKey: key, bundleId: env.AGENTHAIL_IOS_BUNDLE_ID, buildNumber: args.build, marketingVersion: args.version, fetchImpl, now, timeoutMs: Number(env.ASC_GATE_TIMEOUT_SECONDS || 60) * 1000, intervalMs: Number(env.ASC_GATE_POLL_SECONDS || 5) * 1000, onStatus: message => process.stderr.write(`${message}\n`) })
      result.internalGate = { buildId: gate.build.id, internalBuildState: gate.internalBuildState, groupId: gate.groupId }
      result.ready = true
    }
  }
  else if (args.action === "assign-build") result = await manager.assignBuild(args.version, args.build)
  else if (args.action === "remove-build") result = await manager.removeBuild(args.version, args.build)
  else if (args.action === "invite-existing-tester") result = await manager.inviteExistingTester(args.email)
  else if (args.action === "remove-tester") result = await manager.removeTester(args.email)
  else if (args.action === "resend-invitation") result = await manager.resendInvitation(args.email)
  else if (args.action === "notes") result = await manager.notes(args.version, args.build, args.locale || "en-US", args.notes)
  else if (args.action === "expire-build") result = await manager.expireBuild(args.version, args.build)
  else throw new TestFlightError("usage: status | assign-build | remove-build | invite-existing-tester | remove-tester | resend-invitation | notes | expire-build")
  stdout.write(`${JSON.stringify(result)}\n`)
  return result
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  runCLI().catch(error => { process.stderr.write(`${error.message}\n`); process.exitCode = 1 })
}
