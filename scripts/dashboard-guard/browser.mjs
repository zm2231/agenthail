import fs from "node:fs";
import path from "node:path";
import { Browser, computeExecutablePath, detectBrowserPlatform, install } from "@puppeteer/browsers";
import puppeteer from "puppeteer-core";

export const CHROME_BUILD = "156.0.8078.47";

export async function launch() {
  const cacheDir = process.env.AGENTHAIL_BROWSER_CACHE;
  if (!cacheDir) throw new Error("set AGENTHAIL_BROWSER_CACHE to the Chrome for Testing cache directory");
  const options = { browser: Browser.CHROMEHEADLESSSHELL, buildId: CHROME_BUILD, cacheDir, platform: detectBrowserPlatform() };
  let executablePath = computeExecutablePath(options);
  if (!fs.existsSync(executablePath)) executablePath = (await install(options)).executablePath;
  const profile = fs.mkdtempSync(path.join(process.env.TMPDIR || "/tmp", "agenthail-guard-profile-"));
  const browser = await puppeteer.launch({
    executablePath,
    userDataDir: profile,
    headless: "shell",
    args: ["--no-first-run", "--no-default-browser-check", "--disable-gpu-compositing", "--force-color-profile=srgb", "--font-render-hinting=none"],
  });
  return {
    browser,
    async close() {
      await browser.close();
      fs.rmSync(profile, { recursive: true, force: true });
    },
  };
}
