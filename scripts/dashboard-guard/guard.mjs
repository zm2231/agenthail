import path from "node:path";
import { launch } from "./browser.mjs";
import { decode, meanLuminance, QUANTIZATION, worstContrast } from "./contrast.mjs";
import { collectText, HIDE_TEXT } from "./text.mjs";
import { openView, settle, VIEWS } from "./views.mjs";

const dashboards = JSON.parse(process.env.AGENTHAIL_GUARD_DASHBOARDS);
const shots = process.env.AGENTHAIL_GUARD_SHOTS;
const CANDIDATES = ["art: field", "art: tide", "art: aurora", "art: dawn", "gradient: dusk", "gradient: slate", "gradient: forest", "gradient: ember", "gradient: violet", "gradient: mono"];
const SIZES = { wide: [1440, 900], phone: [390, 844] };
const PHONE_VIEWS = new Set(["overview", "session open"]);

async function setUI(dashboard, lines) {
  const response = await fetch(`${dashboard.url}/api/v1/ui`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${dashboard.token}` },
    body: JSON.stringify({ action: "set", set: lines }),
  });
  if (!response.ok) throw new Error(`set ui ${lines.join("; ")}: ${response.status} ${await response.text()}`);
}

async function signedIn(browser, dashboard) {
  const context = await browser.createBrowserContext();
  const [name, value] = dashboard.cookie.split("=");
  await context.setCookie({ name, value, url: dashboard.url });
  return context.newPage();
}

async function brightest(page, dashboard, theme) {
  let best = null;
  await page.setViewport({ width: SIZES.wide[0], height: SIZES.wide[1] });
  for (const background of CANDIDATES) {
    await setUI(dashboard, [`theme = ${theme}`, `background = ${background}`]);
    await page.goto(`${dashboard.url}/#overview`, { waitUntil: "load" });
    await settle(page);
    const hidden = await page.addStyleTag({ content: ".shell { visibility: hidden !important; }" });
    const mean = meanLuminance(decode(await page.screenshot()));
    await hidden.evaluate((node) => node.remove());
    if (!best || mean > best.mean) best = { background, mean };
  }
  return best.background;
}

async function check(page, label, target) {
  const failures = [];
  const texts = await page.evaluate(collectText);
  for (const entry of texts) {
    if (!entry.pane || !entry.inside) failures.push(`${label}: "${entry.text}" (${entry.where}) sits ${entry.pane ? `outside its pane ${entry.pane}` : "on no pane"}`);
  }
  const hidden = await page.addStyleTag({ content: HIDE_TEXT });
  const ground = decode(await page.screenshot());
  await hidden.evaluate((node) => node.remove());
  await settle(page);
  for (const entry of texts) {
    if (entry.inactive || !entry.colour) continue;
    const { worst, at } = worstContrast(entry, ground, 1);
    if (worst < target - QUANTIZATION) failures.push(`${label}: "${entry.text}" (${entry.where}) reads at ${worst.toFixed(2)}:1 at ${at}, below ${target}:1`);
  }
  return failures;
}

async function checkView(page, label, target) {
  const failures = await check(page, `${label} top`, target);
  const scrolls = await page.evaluate(() => document.documentElement.scrollHeight > innerHeight + 1);
  if (!scrolls) return failures;
  await page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight));
  await settle(page);
  return failures.concat(await check(page, `${label} bottom`, target));
}

const started = Date.now();
const { browser, close } = await launch();
const failures = [];
try {
  const pages = {};
  for (const [key, dashboard] of Object.entries(dashboards)) pages[key] = await signedIn(browser, dashboard);
  const backgrounds = {};
  for (const theme of ["dark", "light"]) backgrounds[theme] = [...new Set(["art: field", await brightest(pages.full, dashboards.full, theme)])];
  console.log(`backgrounds: dark ${backgrounds.dark.join(", ")}; light ${backgrounds.light.join(", ")}`);
  for (const theme of ["dark", "light"]) {
    for (const background of backgrounds[theme]) {
      for (const dashboard of Object.values(dashboards)) await setUI(dashboard, [`theme = ${theme}`, `background = ${background}`]);
      for (const contrast of ["no-preference", "more"]) {
        const target = contrast === "more" ? 7 : 4.5;
        for (const [size, [width, height]] of Object.entries(SIZES)) {
          if (size === "phone" && contrast === "more") continue;
          for (const view of VIEWS) {
            if (size === "phone" && !PHONE_VIEWS.has(view.name)) continue;
            const page = pages[view.dashboard];
            await page.setViewport({ width, height });
            await page.emulateMediaFeatures([{ name: "prefers-contrast", value: contrast }]);
            await openView(page, dashboards[view.dashboard], view);
            const label = `${theme} ${background} ${contrast === "more" ? "more contrast " : ""}${width}x${height} ${view.name}`;
            failures.push(...(await checkView(page, label, target)));
            if (shots) await page.screenshot({ path: path.join(shots, `${label.replace(/\W+/g, "-")}.png`) });
          }
        }
      }
    }
  }
} finally {
  await close();
}
console.log(`readability guard: ${failures.length} failures in ${((Date.now() - started) / 1000).toFixed(0)} s`);
for (const failure of failures) console.log(failure);
process.exitCode = failures.length ? 1 : 0;
