// Generic cookie bridge for agenthail. Prints "name=value; ..." for the given
// URL from Chrome. Usage: node cookie.mjs <url>
//
// With env AGENTHAIL_CHROME_PROFILE set, uses that Chrome profile.
import { getCookies, toCookieHeader } from "@steipete/sweet-cookie";

const url = process.argv[2] || "https://claude.ai/";

try {
  const opts = { url };
  const profile = process.env.AGENTHAIL_CHROME_PROFILE;
  if (profile) opts.profile = profile;
  const r = await getCookies(opts);
  if (!r.cookies.length) {
    process.stderr.write(`no cookies for ${url}\n`);
    process.exit(2);
  }
  process.stdout.write(toCookieHeader(r.cookies));
} catch (e) {
  process.stderr.write(`cookie read error: ${e.message}\n`);
  process.exit(1);
}