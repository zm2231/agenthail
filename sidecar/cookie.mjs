// Cookie bridge for rc.py. Prints "name=value; ..." for claude.ai from the
// given Chrome profile (default "Default"). Reads via @steipete/sweet-cookie.
//
// With arg "raw" as the first param, prints one JSON line per cookie with
// name/expires/length instead of a header string (used by `doctor`).
import { getCookies, toCookieHeader } from "@steipete/sweet-cookie";

const mode = process.argv[2];
let profile = process.argv[3] || "Default";
if (mode !== "raw") profile = mode; // normal mode: argv[2] = profile

try {
  const r = await getCookies({ url: "https://claude.ai/", profile });
  if (mode === "raw") {
    for (const c of r.cookies) {
      process.stdout.write(JSON.stringify({ name: c.name, expires: c.expires, len: String(c.value).length }) + "\n");
    }
  } else {
    if (!r.cookies.some((c) => c.name === "sessionKey")) {
      process.stderr.write(`no sessionKey in profile '${profile}'\n`);
      process.exit(2);
    }
    process.stdout.write(toCookieHeader(r.cookies));
  }
} catch (e) {
  process.stderr.write(`cookie read error: ${e.message}\n`);
  process.exit(1);
}
