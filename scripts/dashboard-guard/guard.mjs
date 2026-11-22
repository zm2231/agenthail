import fs from "node:fs";
import path from "node:path";
import { launch } from "./browser.mjs";
import { brightLuminance, decode, QUANTIZATION, worstContrast } from "./contrast.mjs";
import { describeRequests, trackRequests } from "./requests.mjs";
import { collectText, HIDE_TEXT } from "./text.mjs";
import { awaited, onStep, openView, settle, step, VIEWS } from "./views.mjs";

const themes = JSON.parse(process.env.AGENTHAIL_GUARD_DASHBOARDS);
const shots = process.env.AGENTHAIL_GUARD_SHOTS;
const CANDIDATES = ["art: field", "art: tide", "art: aurora", "art: dawn", "gradient: dusk", "gradient: slate", "gradient: forest", "gradient: ember", "gradient: violet", "gradient: mono"];
const SIZES = { wide: [1440, 900], phone: [390, 844] };
const STALL_LIMIT_MS = 3 * 60_000;
const progress = new Map(Object.keys(themes).map((theme) => [theme, { last: Date.now(), longest: 0, doing: "starting", pages: {} }]));
const steps = new Map();
const requests = new Map();
onStep((page, name) => steps.set(page, name));

function working(theme, label) {
  const now = Date.now();
  const mine = progress.get(theme);
  mine.longest = Math.max(mine.longest, now - mine.last);
  mine.last = now;
  mine.doing = label;
}
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
  const page = await context.newPage();
  page.setDefaultTimeout(0);
  requests.set(page, { url: dashboard.url, pending: await trackRequests(page) });
  return page;
}

async function brightest(page, dashboard, theme) {
  let best = null;
  await step(page, "brightest viewport", () => page.setViewport({ width: SIZES.wide[0], height: SIZES.wide[1] }));
  for (const background of CANDIDATES) {
    working(theme, `brightest ${background}`);
    await step(page, "brightest set ui", () => setUI(dashboard, [`theme = ${theme}`, `background = ${background}`]));
    await step(page, "brightest blank", () => page.goto("about:blank"));
    await step(page, "brightest load", () => page.goto(`${dashboard.url}/#overview`, { waitUntil: "load" }));
    await settle(page);
    const hidden = await step(page, "brightest hide shell", () => page.addStyleTag({ content: ".shell { visibility: hidden !important; }" }));
    const bright = brightLuminance(decode(await step(page, "brightest screenshot", () => page.screenshot())));
    await step(page, "brightest show shell", () => hidden.evaluate((node) => node.remove()));
    if (!best || bright > best.bright) best = { background, bright };
  }
  return best.background;
}

async function capture(page) {
  let moved = "";
  for (let attempt = 0; attempt < 3; attempt++) {
    const texts = await step(page, "check text", () => page.evaluate(collectText));
    const shown = decode(await step(page, "check screenshot", () => page.screenshot()));
    const hidden = await step(page, "check hide text", () => page.addStyleTag({ content: HIDE_TEXT }));
    const ground = decode(await step(page, "check ground", () => page.screenshot()));
    await step(page, "check show text", () => hidden.evaluate((node) => node.remove()));
    await settle(page);
    const after = await step(page, "check text again", () => page.evaluate(collectText));
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
  if (failures.length) failures.push(`${label}: root style ${await step(page, "check root style", () => page.evaluate(() => document.documentElement.getAttribute("style")))}`);
  return failures;
}

async function checkView(page, label, target) {
  const failures = await check(page, `${label} top`, target);
  const scrolls = await step(page, "check scroll height", () => page.evaluate(() => document.documentElement.scrollHeight > innerHeight + 1));
  if (!scrolls) return failures;
  await step(page, "check scroll", () => page.evaluate(() => scrollTo(0, document.documentElement.scrollHeight)));
  await settle(page);
  return failures.concat(await check(page, `${label} bottom`, target));
}

function roleOverrides() {
  const style = document.documentElement.style;
  return ["--ink", "--muted", "--quiet", "--accent", "--good", "--warn", "--bad"].map((role) => `${role}: ${style.getPropertyValue(role) || "palette"}`).join("; ");
}

async function themeSurvivesHiddenText(page, dashboard, label) {
  await openView(page, dashboard, { hash: "overview", dashboard: "full" });
  const before = await step(page, "theme pass roles", () => page.evaluate(roleOverrides));
  const hidden = await step(page, "theme pass hide text", () => page.addStyleTag({ content: HIDE_TEXT }));
  const passStarted = await step(page, "theme pass resize", () =>
    page.evaluate(() => {
      dispatchEvent(new Event("resize"));
      return !document.documentElement.hasAttribute("data-ui-settled");
    }),
  );
  if (!passStarted) {
    await step(page, "theme pass show text", () => hidden.evaluate((node) => node.remove()));
    return [`${label}: a resize started no theme pass`];
  }
  await settle(page);
  await step(page, "theme pass show text", () => hidden.evaluate((node) => node.remove()));
  await settle(page);
  const after = await step(page, "theme pass roles again", () => page.evaluate(roleOverrides));
  return before === after ? [] : [`${label}: a theme pass while text was hidden solved text colours as "${after}", not "${before}"`];
}

async function guardTheme(browser, theme, dashboards) {
  const failures = [];
  const pages = {};
  progress.get(theme).pages = pages;
  for (const [key, dashboard] of Object.entries(dashboards)) {
    working(theme, `sign in ${key}`);
    pages[key] = await signedIn(browser, dashboard);
  }
  const backgrounds = [...new Set(["art: field", await brightest(pages.full, dashboards.full, theme)])];
  console.log(`${theme} backgrounds: ${backgrounds.join(", ")}`);
  const solved = new Map();
  for (const background of backgrounds) {
    for (const [key, dashboard] of Object.entries(dashboards)) await step(pages[key], "set ui", () => setUI(dashboard, [`theme = ${theme}`, `background = ${background}`]));
    for (const contrast of ["no-preference", "more"]) {
      const target = contrast === "more" ? 7 : 4.5;
      await step(pages.full, "viewport", () => pages.full.setViewport({ width: SIZES.wide[0], height: SIZES.wide[1] }));
      await step(pages.full, "contrast", () => pages.full.emulateMediaFeatures([{ name: "prefers-contrast", value: contrast }]));
      working(theme, `${theme} ${background} ${contrast === "more" ? "more contrast " : ""}theme pass`);
      failures.push(...(await themeSurvivesHiddenText(pages.full, dashboards.full, `${theme} ${background} ${contrast === "more" ? "more contrast " : ""}theme pass`)));
      for (const [width, height] of Object.values(SIZES)) {
        for (const view of VIEWS) {
          if (view.wideOnly && width < SIZES.wide[0]) continue;
          const page = pages[view.dashboard];
          await step(page, "viewport", () => page.setViewport({ width, height }));
          await step(page, "contrast", () => page.emulateMediaFeatures([{ name: "prefers-contrast", value: contrast }]));
          const label = `${theme} ${background} ${contrast === "more" ? "more contrast " : ""}${width}x${height} ${view.name}`;
          working(theme, label);
          try {
            await openView(page, dashboards[view.dashboard], view);
            const roles = await step(page, "roles", () => page.evaluate(roleOverrides));
            const key = `${background} ${contrast}`;
            if (!solved.has(key)) solved.set(key, roles);
            else if (solved.get(key) !== roles) failures.push(`${label}: text colours solved as "${roles}", not "${solved.get(key)}" as on other views`);
            failures.push(...(await checkView(page, label, target)));
          } catch (error) {
            if (shots) await step(page, "failure screenshot", () => page.screenshot({ path: path.join(shots, `failed-${label.replace(/\W+/g, "-")}.png`) })).catch(() => {});
            throw new Error(`${label}: ${error.message}`);
          }
          if (shots) await step(page, "save screenshot", () => page.screenshot({ path: path.join(shots, `${label.replace(/\W+/g, "-")}.png`) }));
        }
      }
    }
  }
  working(theme, "done");
  return failures;
}

const started = Date.now();
const { browser, close, abandon } = await launch();
const pageState = (page) =>
  Promise.race([
    page.evaluate(async (selector) => {
      const html = document.documentElement;
      const frame = await Promise.race([new Promise((resolve) => requestAnimationFrame(() => resolve("fires"))), new Promise((resolve) => setTimeout(() => resolve("none in 2 s"), 2000))]);
      const describe = (element) => element && `${element.localName}${element.id ? `#${element.id}` : ""}${[...element.classList].map((name) => `.${name}`).join("")}`;
      const target = selector && document.querySelector(selector);
      const style = target && getComputedStyle(target);
      const box = target?.getBoundingClientRect();
      const wait = selector && (target ? { selector, exists: true, visible: !["hidden", "collapse"].includes(style.visibility) && box.width > 0 && box.height > 0, display: style.display, visibility: style.visibility, size: `${box.width}x${box.height}`, hidden: target.hidden ?? null } : { selector, exists: false });
      const focus = document.activeElement;
      const focused = { element: describe(focus), value: typeof focus?.value === "string" ? focus.value.slice(0, 40) : null };
      return JSON.stringify({ url: location.href, ready: document.readyState, uiSettled: html.hasAttribute("data-ui-settled"), status: document.querySelector("#daemon-status")?.textContent ?? null, visibility: document.visibilityState, frame, toastHidden: document.querySelector("#toast")?.hidden ?? null, wait, focused });
    }, awaited(page)),
    new Promise((resolve) => setTimeout(() => resolve("did not answer in 30 s"), 30_000)),
  ]).catch((error) => `unreadable: ${error.message}`);

let stopping = false;
// The test times out a minute after the deadline, so the stall file goes first and pages are probed at once.
async function stop(reason, stuckThemes) {
  stopping = true;
  console.log(`readability guard: ${reason}`);
  const pages = stuckThemes.flatMap(([theme, mine]) => Object.entries(mine.pages).map(([key, page]) => ({ theme, key, page, ...(requests.get(page) ?? { url: null, pending: () => [] }) })));
  const stuck = pages.flatMap(({ url, pending }) => pending().map((entry) => ({ dashboard: url, id: entry.id, path: entry.path })));
  if (process.env.AGENTHAIL_GUARD_STALL_FILE) fs.writeFileSync(process.env.AGENTHAIL_GUARD_STALL_FILE, JSON.stringify(stuck));
  const states = await Promise.all(pages.map(({ page }) => pageState(page)));
  pages.forEach(({ theme, key, page, pending }, index) => {
    console.log(`readability guard: ${theme} ${key} page at ${steps.get(page) ?? "no step"}, ${states[index]}`);
    console.log(`readability guard: ${theme} ${key} page requests: ${describeRequests(pending())}`);
  });
  abandon();
  process.exit(1);
}

const deadline = Number(process.env.AGENTHAIL_GUARD_DEADLINE);
if (deadline) {
  setTimeout(() => {
    if (stopping) return;
    stop(`not finished by its deadline after ${((Date.now() - started) / 1000).toFixed(0)} s`, [...progress].filter(([, mine]) => mine.doing !== "done"));
  }, Math.max(deadline - Date.now(), 0)).unref();
}
setInterval(() => {
  const now = Date.now();
  const stalled = [...progress].find(([, mine]) => mine.doing !== "done" && now - mine.last > STALL_LIMIT_MS);
  if (!stalled || stopping) return;
  stop(`${stalled[0]} finished nothing in ${STALL_LIMIT_MS / 1000} s, stuck on ${stalled[1].doing}`, [stalled]);
}, 1000).unref();

let failures = [];
try {
  const results = await Promise.all(Object.entries(themes).map(([theme, dashboards]) => guardTheme(browser, theme, dashboards)));
  failures = results.flat();
} finally {
  await close();
}
console.log(`readability guard: ${failures.length} failures in ${((Date.now() - started) / 1000).toFixed(0)} s, at most ${(Math.max(...[...progress.values()].map((mine) => mine.longest)) / 1000).toFixed(0)} s between finished steps`);
for (const failure of failures) console.log(failure);
process.exitCode = failures.length ? 1 : 0;
