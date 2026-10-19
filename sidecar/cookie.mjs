// Prints "name=value; ..." for a URL from one browser profile and nothing
// else: only the named browser is read.
// Usage: node cookie.mjs --browser <name> [--profile-dir <path>] <url>
import { getCookies, toCookieHeader } from "@steipete/sweet-cookie";

const chromiumBrowsers = new Set(["chrome", "brave", "arc", "chromium"]);

function parseArgs(argv) {
  const parsed = { browser: "", profileDir: "", url: "" };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--browser") parsed.browser = argv[++i] ?? "";
    else if (arg === "--profile-dir") parsed.profileDir = argv[++i] ?? "";
    else if (arg.startsWith("--")) throw new Error(`unknown option ${arg}`);
    else parsed.url = arg;
  }
  return parsed;
}

function cookieOptions({ browser, profileDir, url }) {
  if (!url) throw new Error("a URL is required");
  const options = { url, mode: "first" };
  if (chromiumBrowsers.has(browser)) {
    if (!profileDir) throw new Error(`--profile-dir is required for ${browser}`);
    options.browsers = ["chrome"];
    options.chromeProfile = profileDir;
    if (process.platform === "darwin") options.chromiumBrowser = browser;
  } else if (browser === "edge") {
    if (!profileDir) throw new Error("--profile-dir is required for edge");
    options.browsers = ["edge"];
    options.edgeProfile = profileDir;
  } else if (browser === "firefox") {
    if (!profileDir) throw new Error("--profile-dir is required for firefox");
    options.browsers = ["firefox"];
    options.firefoxProfile = profileDir;
  } else if (browser === "safari") {
    options.browsers = ["safari"];
  } else {
    throw new Error(`unsupported browser ${JSON.stringify(browser)}`);
  }
  return options;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const result = await getCookies(cookieOptions(args));
  if (!result.cookies.length) {
    const detail = result.warnings.length ? `: ${result.warnings.join("; ")}` : "";
    process.stderr.write(`no ${args.browser} cookies for ${args.url}${detail}\n`);
    process.exit(2);
  }
  process.stdout.write(toCookieHeader(result.cookies));
}

main().catch((error) => {
  process.stderr.write(`cookie read error: ${error.message}\n`);
  process.exit(1);
});
