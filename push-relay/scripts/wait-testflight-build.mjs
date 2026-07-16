import { createPrivateKey, sign } from "node:crypto"
import { readFile } from "node:fs/promises"
import { homedir } from "node:os"
import { pathToFileURL } from "node:url"

const apiBase = "https://api.appstoreconnect.apple.com"

function encodeJSON(value) {
  return Buffer.from(JSON.stringify(value)).toString("base64url")
}

export function createAppStoreToken({ issuerId, keyId, privateKey, now = Date.now() }) {
  const issuedAt = Math.floor(now / 1000) - 30
  const unsigned = `${encodeJSON({ alg: "ES256", kid: keyId, typ: "JWT" })}.${encodeJSON({ iss: issuerId, iat: issuedAt, exp: issuedAt + 600, aud: "appstoreconnect-v1" })}`
  const signature = sign("sha256", Buffer.from(unsigned), { key: createPrivateKey(privateKey), dsaEncoding: "ieee-p1363" })
  return `${unsigned}.${signature.toString("base64url")}`
}

export function appLookupURL(bundleId, baseURL = apiBase) {
  const url = new URL("/v1/apps", baseURL)
  url.searchParams.set("filter[bundleId]", bundleId)
  url.searchParams.set("fields[apps]", "bundleId,name")
  url.searchParams.set("limit", "2")
  return url
}

export function buildLookupURL({ appId, buildNumber, marketingVersion }, baseURL = apiBase) {
  const url = new URL("/v1/builds", baseURL)
  url.searchParams.set("filter[app]", appId)
  url.searchParams.set("filter[version]", buildNumber)
  url.searchParams.set("filter[preReleaseVersion.platform]", "IOS")
  url.searchParams.set("filter[preReleaseVersion.version]", marketingVersion.replace(/^v/, ""))
  url.searchParams.set("fields[builds]", "version,uploadedDate,processingState,expired")
  url.searchParams.set("sort", "-uploadedDate")
  url.searchParams.set("limit", "2")
  return url
}

async function fetchJSON(url, { fetchImpl, token, requestTimeoutMs = 15_000 }) {
  let response
  try {
    response = await fetchImpl(url, { headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(requestTimeoutMs) })
  } catch (cause) {
    const error = new Error(`App Store Connect request failed: ${cause.message}`)
    error.retryable = true
    throw error
  }
  const body = await response.text()
  if (response.ok) {
    try {
      return body ? JSON.parse(body) : {}
    } catch (cause) {
      const error = new Error(`App Store Connect returned invalid JSON: ${cause.message}`)
      error.retryable = true
      throw error
    }
  }
  const error = new Error(`App Store Connect returned ${response.status}: ${body.slice(0, 500)}`)
  error.retryable = response.status === 429 || response.status >= 500
  throw error
}

export async function waitForProcessedBuild({
  issuerId,
  keyId,
  privateKey,
  bundleId,
  buildNumber,
  marketingVersion,
  baseURL = apiBase,
  timeoutMs = 30 * 60 * 1000,
  intervalMs = 15 * 1000,
  fetchImpl = fetch,
  now = Date.now,
  sleep = ms => new Promise(resolve => setTimeout(resolve, ms)),
  onStatus = message => process.stdout.write(`${message}\n`)
}) {
  const deadline = now() + timeoutMs
  let appId = ""
  let lastStatus = ""
  while (now() < deadline) {
    try {
      const token = createAppStoreToken({ issuerId, keyId, privateKey, now: now() })
      if (!appId) {
        const apps = await fetchJSON(appLookupURL(bundleId, baseURL), { fetchImpl, token })
        if (apps.data?.length !== 1) throw new Error(`Expected one App Store app for ${bundleId}, found ${apps.data?.length || 0}`)
        appId = apps.data[0].id
      }
      const builds = await fetchJSON(buildLookupURL({ appId, buildNumber, marketingVersion }, baseURL), { fetchImpl, token })
      const build = builds.data?.[0]
      const state = build?.attributes?.processingState || "WAITING_FOR_UPLOAD"
      if (state !== lastStatus) {
        onStatus(`TestFlight build ${marketingVersion} (${buildNumber}): ${state}`)
        lastStatus = state
      }
      if (state === "VALID") {
        if (build.attributes.expired) throw new Error(`TestFlight build ${buildNumber} is already expired`)
        return build
      }
      if (state === "FAILED" || state === "INVALID") throw new Error(`TestFlight processing ended in ${state}`)
    } catch (error) {
      if (!error.retryable) throw error
      onStatus(`TestFlight status check will retry: ${error.message}`)
    }
    await sleep(Math.min(intervalMs, Math.max(0, deadline - now())))
  }
  throw new Error(`Timed out waiting for TestFlight build ${marketingVersion} (${buildNumber})`)
}

async function main() {
  const keyId = process.env.ASC_KEY_ID
  const issuerId = process.env.ASC_ISSUER_ID
  const bundleId = process.env.ASC_BUNDLE_ID || "com.agenthail.ios"
  const buildNumber = process.env.ASC_BUILD_NUMBER
  const marketingVersion = process.env.ASC_MARKETING_VERSION
  const keyPath = process.env.ASC_PRIVATE_KEY_PATH || `${homedir()}/.appstoreconnect/private_keys/AuthKey_${keyId}.p8`
  for (const [name, value] of Object.entries({ ASC_KEY_ID: keyId, ASC_ISSUER_ID: issuerId, ASC_BUILD_NUMBER: buildNumber, ASC_MARKETING_VERSION: marketingVersion })) {
    if (!value) throw new Error(`missing ${name}`)
  }
  const privateKey = await readFile(keyPath, "utf8")
  await waitForProcessedBuild({
    issuerId,
    keyId,
    privateKey,
    bundleId,
    buildNumber,
    marketingVersion,
    timeoutMs: Number(process.env.ASC_TIMEOUT_SECONDS || 1800) * 1000,
    intervalMs: Number(process.env.ASC_POLL_SECONDS || 15) * 1000
  })
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  })
}
