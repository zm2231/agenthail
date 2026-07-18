import { readFileSync } from "node:fs"
import http2 from "node:http2"
import { pathToFileURL } from "node:url"

const encoder = new TextEncoder()

export async function createProviderToken({ keyID, teamID, privateKey, now = Math.floor(Date.now() / 1000) }) {
  const header = base64url(encoder.encode(JSON.stringify({ alg: "ES256", kid: keyID })))
  const claims = base64url(encoder.encode(JSON.stringify({ iss: teamID, iat: now })))
  const signingInput = `${header}.${claims}`
  const key = await crypto.subtle.importKey("pkcs8", pemBytes(privateKey), { name: "ECDSA", namedCurve: "P-256" }, false, ["sign"])
  const signature = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, key, encoder.encode(signingInput))
  return `${signingInput}.${base64url(new Uint8Array(signature))}`
}

export function acceptedProbe(status, reason) {
  return status === 400 && reason === "BadDeviceToken"
}

export async function probeAPNs({ keyID, teamID, topic, privateKey, host = "api.push.apple.com" }) {
  const providerToken = await createProviderToken({ keyID, teamID, privateKey })
  const deviceToken = "0".repeat(64)
  const client = http2.connect(`https://${host}`)
  return await new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      client.destroy()
      reject(new Error("APNs credential probe timed out"))
    }, 15000)
    const request = client.request({
      ":method": "POST",
      ":path": `/3/device/${deviceToken}`,
      authorization: `bearer ${providerToken}`,
      "apns-topic": topic,
      "apns-push-type": "alert",
      "content-type": "application/json"
    })
    let status = 0
    let body = ""
    request.setEncoding("utf8")
    request.on("response", headers => { status = Number(headers[":status"] || 0) })
    request.on("data", chunk => { body += chunk })
    request.on("error", error => {
      clearTimeout(timer)
      client.destroy()
      reject(error)
    })
    request.on("end", () => {
      clearTimeout(timer)
      client.close()
      let reason = ""
      try { reason = JSON.parse(body).reason || "" } catch {}
      resolve({ status, reason })
    })
    request.end(JSON.stringify({ aps: { alert: "Agenthail release probe" } }))
  })
}

async function main() {
  const keyID = required("APNS_KEY_ID")
  const teamID = required("APPLE_TEAM_ID")
  const topic = required("APNS_TOPIC")
  const keyFile = process.env.APNS_KEY_FILE
  const privateKey = keyFile ? readFileSync(keyFile, "utf8") : required("APNS_KEY_P8")
  const result = await probeAPNs({ keyID, teamID, topic, privateKey })
  if (!acceptedProbe(result.status, result.reason)) {
    throw new Error(`APNs rejected the configured provider identity: status=${result.status} reason=${result.reason || "unknown"}`)
  }
  process.stdout.write("APNs provider identity accepted by Apple.\n")
}

function required(name) {
  const value = process.env[name]?.trim()
  if (!value) throw new Error(`Missing ${name}`)
  return value
}

function pemBytes(value) {
  const body = value.replace(/-----BEGIN PRIVATE KEY-----|-----END PRIVATE KEY-----|\s/g, "")
  return Buffer.from(body, "base64")
}

function base64url(value) {
  return Buffer.from(value).toString("base64url")
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stderr.write(`${error.message}\n`)
    process.exitCode = 1
  })
}
