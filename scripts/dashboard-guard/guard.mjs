import path from "node:path";
import { launch } from "./browser.mjs";
import { brightLuminance, decode, QUANTIZATION, worstContrast } from "./contrast.mjs";
import { collectText, HIDE_TEXT } from "./text.mjs";
import { openView, settle, VIEWS } from "./views.mjs";

const themes = JSON.parse(process.env.AGENTHAIL_GUARD_DASHBOARDS);
const shots = process.env.AGENTHAIL_GUARD_SHOTS;
const CANDIDATES = ["art: field", "art: tide", "art: aurora", "art: dawn", "gradient: dusk", "gradient: slate", "gradient: forest", "gradient: ember", "gradient: violet", "gradient: mono"];
const SIZES = { wide: [1440, 900], phone: [390, 844] };
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
    await page.goto("about:blank");
    await page.goto(`${dashboard.url}/#overview`, { waitUntil: "load" });
    await settle(page);
    const hidden = await page.addStyleTag({ content: ".shell { visibility: hidden !important; }" });
    const bright = brightLuminance(decode(await page.screenshot()));
    await hidden.evaluate((node) => node.remove());
    if (!best || bright > best.bright) best = { background, bright };
  }
  return best.background;
}

async function capture(page) {
  let moved = "";
  for (let attempt = 0; attempt < 3; attempt++) {
    const texts = await page.evaluate(collectText);
    const shown = decode(await page.screenshot());
    const hidden = await page.addStyleTag({ content: HIDE_TEXT });
    const ground = decode(await page.screenshot());
    await hidden.evaluate((node) => node.remove());
    await settle(page);
    const after = await page.evaluate(collectText);
    const index = texts.findIndex((entry, i) => JSON.stringify(entry) !== JSON.stringify(after[i]));
    if (index === -1 && texts.length === after.length) return { texts, ground, shown };
    moved = index === -1 ? `${texts.length} texts became ${after.length}` : `${JSON.stringify(texts[index])} became ${JSON.stringify(after[index])}`;
  }
  throw new Error(`layout kept moving while capturing: ${moved}`);
}

async function check(page, label, target) {
  const failures = [];
  const { texts, ground, shown } = await capture(page);
  for (const entry of texts) {
    if (!entry.pane || !entry.inside) failures.push(`${label}: "${entry.text}" (${entry.where}) sits ${entry.pane ? `outside its pane ${entry.pane}` : "on no pane"}`);
  }
  for (const entry of texts) {
    if (entry.inactive) continue;
    const { worst, at, over } = worstContrast(entry, ground, 1, shown);
    if (worst < target - QUANTIZATION) failures.push(`${label}: "${entry.text}" (${entry.where}) reads at ${worst.toFixed(2)}:1 at ${at} as rgb(${entry.colour.slice(0, 3).map((v) => Math.round(v * 255)).join(" ")} / ${(entry.colour[3] * entry.opacity).toFixed(2)}) over ${over}, below ${target}:1`);
  }
  if (failures.length) failures.push(`${label}: root style ${await page.evaluate(() => document.documentElement.getAttribute("style"))}`);
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

function roleOverrides() {
  const style = document.documentElement.style;
  return ["--ink", "--muted", "--quiet", "--accent", "--good", "--warn", "--bad"].map((role) => `${role}: ${style.getPropertyValue(role) || "palette"}`).join("; ");
}

async function themeSurvivesHiddenText(page, dashboard, label) {
  await openView(page, dashboard, { hash: "overview", dashboard: "full" });
  const before = await page.evaluate(roleOverrides);
  const hidden = await page.addStyleTag({ content: HIDE_TEXT });
  await page.evaluate(() => dispatchEvent(new Event("resize")));
  await page.waitForFunction(() => !document.documentElement.hasAttribute("data-ui-settled"));
  await settle(page);
  await hidden.evaluate((node) => node.remove());
  await settle(page);
  const after = await page.evaluate(roleOverrides);
  return before === after ? [] : [`${label}: a theme pass while text was hidden solved text colours as "${after}", not "${before}"`];
}

async function guardTheme(browser, theme, dashboards) {
  const failures = [];
  const pages = {};
  for (const [key, dashboard] of Object.entries(dashboards)) pages[key] = await signedIn(browser, dashboard);
  const backgrounds = [...new Set(["art: field", await brightest(pages.full, dashboards.full, theme)])];
  console.log(`${theme} backgrounds: ${backgrounds.join(", ")}`);
  const solved = new Map();
  for (const background of backgrounds) {
    for (const dashboard of Object.values(dashboards)) await setUI(dashboard, [`theme = ${theme}`, `background = ${background}`]);
    for (const contrast of ["no-preference", "more"]) {
      const target = contrast === "more" ? 7 : 4.5;
      await pages.full.setViewport({ width: SIZES.wide[0], height: SIZES.wide[1] });
      await pages.full.emulateMediaFeatures([{ name: "prefers-contrast", value: contrast }]);
      failures.push(...(await themeSurvivesHiddenText(pages.full, dashboards.full, `${theme} ${background} ${contrast === "more" ? "more contrast " : ""}theme pass`)));
      for (const [width, height] of Object.values(SIZES)) {
        for (const view of VIEWS) {
          const page = pages[view.dashboard];
          await page.setViewport({ width, height });
          await page.emulateMediaFeatures([{ name: "prefers-contrast", value: contrast }]);
          const label = `${theme} ${background} ${contrast === "more" ? "more contrast " : ""}${width}x${height} ${view.name}`;
          try {
            await openView(page, dashboards[view.dashboard], view);
            const roles = await page.evaluate(roleOverrides);
            const key = `${background} ${contrast}`;
            if (!solved.has(key)) solved.set(key, roles);
            else if (solved.get(key) !== roles) failures.push(`${label}: text colours solved as "${roles}", not "${solved.get(key)}" as on other views`);
            failures.push(...(await checkView(page, label, target)));
          } catch (error) {
            if (shots) await page.screenshot({ path: path.join(shots, `failed-${label.replace(/\W+/g, "-")}.png`) }).catch(() => {});
            throw new Error(`${label}: ${error.message}`);
          }
          if (shots) await page.screenshot({ path: path.join(shots, `${label.replace(/\W+/g, "-")}.png`) });
        }
      }
    }
  }
  return failures;
}

const started = Date.now();
const { browser, close } = await launch();
let failures = [];
try {
  const results = await Promise.all(Object.entries(themes).map(([theme, dashboards]) => guardTheme(browser, theme, dashboards)));
  failures = results.flat();
} finally {
  await close();
}
console.log(`readability guard: ${failures.length} failures in ${((Date.now() - started) / 1000).toFixed(0)} s`);
for (const failure of failures) console.log(failure);
process.exitCode = failures.length ? 1 : 0;
