const press = async (page, selector) => {
  await page.waitForSelector(selector, { visible: true });
  await page.$eval(selector, (element) => element.click());
};

const operations = (tab) => async (page) => {
  await press(page, `[data-operations-tab="${tab}"]`);
  await page.waitForSelector(`[data-operations-tab="${tab}"].active`);
};

const openSession = async (page) => {
  await press(page, '#session-list [data-session="review"]');
  await page.waitForFunction(() => document.querySelector("#chat-body")?.textContent.includes("contrast target"));
  await page.waitForSelector("#background-panel .background-card", { visible: true });
  await page.waitForSelector("#request-panel:not([hidden])", { visible: true });
};

const projects = async (page) => {
  await press(page, '[data-inbox-mode="projects"]');
  await page.waitForSelector("[data-project-new]", { visible: true });
};

const opened = (trigger, shown) => async (page) => {
  await trigger(page);
  await page.waitForSelector(shown, { visible: true });
};

export const VIEWS = [
  { name: "slash menu", dashboard: "full", hash: "conversations", act: opened(async (page) => (await openSession(page), await page.type("#message", "/")), "#slash-menu") },
  { name: "toast", dashboard: "full", hash: "overview", ready: "#recent-activity > *", act: opened((page) => page.evaluate(() => toast("Sent to review. Its reply appears in Conversations.")), "#toast") },
  { name: "voice dialog", dashboard: "full", hash: "overview", act: opened((page) => press(page, "#voice-orchestrator"), "#voice-dialog[open]") },
  { name: "project dialog", dashboard: "full", hash: "conversations", act: opened(async (page) => (await projects(page), await press(page, "[data-project-new]")), "#project-dialog[open]") },
  { name: "project menu", dashboard: "full", hash: "conversations", act: opened(async (page) => (await projects(page), await press(page, ".project-menu summary")), ".project-menu[open] .project-menu-items") },
  { name: "host picker", dashboard: "full", hash: "conversations", act: opened(async (page) => (await press(page, "#new-conversation-toggle"), await press(page, "#new-conversation-host")), "#new-conversation-host-menu") },
  { name: "overview", dashboard: "full", hash: "overview", ready: "#recent-activity > *" },
  { name: "conversations", dashboard: "full", hash: "conversations", ready: "#session-list [data-session]" },
  { name: "session open", dashboard: "full", hash: "conversations", act: openSession },
  { name: "operations delivery", dashboard: "full", hash: "operations", ready: "#queue-list > *", act: operations("queue") },
  { name: "operations handoffs", dashboard: "full", hash: "operations", ready: "#relay-list > *", act: operations("network") },
  { name: "operations audit", dashboard: "full", hash: "operations", ready: "#history-list > *", act: operations("audit") },
  { name: "operations settings", dashboard: "full", hash: "operations", ready: "#signin-consumers .signin-consumer", act: operations("settings") },
  { name: "empty overview", dashboard: "empty", hash: "overview" },
  { name: "empty conversations", dashboard: "empty", hash: "conversations" },
  { name: "empty operations", dashboard: "empty", hash: "operations", act: operations("queue") },
  { name: "empty audit", dashboard: "empty", hash: "operations", act: operations("audit") },
];

const LOADED = { full: "#usage-line .usage-meter" };

export async function openView(page, dashboard, view) {
  await page.goto("about:blank");
  await page.goto(`${dashboard.url}/#${view.hash}`, { waitUntil: "load" });
  await page.waitForFunction(() => document.querySelector("#daemon-status")?.textContent !== "Connecting");
  await page.addStyleTag({ content: "*, *::before, *::after { transition: none !important; }" });
  if (LOADED[view.dashboard]) await page.waitForSelector(LOADED[view.dashboard]);
  if (view.act) await view.act(page);
  if (view.ready) await page.waitForSelector(view.ready, { visible: true });
  await settle(page);
}

const frames = () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));

async function step(name, run) {
  try {
    return await run();
  } catch (error) {
    throw new Error(`settle ${name}: ${error.message}`);
  }
}

export async function settle(page) {
  await step("frames", () => page.evaluate(frames));
  await step("ui", () => page.waitForSelector("html[data-ui-settled]"));
  await step("fonts", () => page.evaluate(() => document.fonts.ready.then(() => {})));
  await step("animations", () =>
    page.evaluate(async () => {
      const running = document.getAnimations().filter((animation) => animation.playState === "running" && Number.isFinite(animation.effect?.getComputedTiming().endTime));
      await Promise.all(running.map((animation) => animation.finished.catch(() => {})));
    }),
  );
  await step("frames", () => page.evaluate(frames));
}
