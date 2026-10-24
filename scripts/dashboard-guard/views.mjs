const operations = (tab) => async (page) => {
  await page.$eval(`[data-operations-tab="${tab}"]`, (button) => button.click());
  await page.waitForSelector(`[data-operations-tab="${tab}"].active`);
};

const openSession = async (page) => {
  await page.waitForSelector('#session-list [data-session="review"]', { visible: true });
  await page.click('#session-list [data-session="review"]');
  await page.waitForFunction(() => document.querySelector("#chat-body")?.textContent.includes("contrast target"));
  await page.waitForSelector("#background-panel .background-card", { visible: true });
  await page.waitForSelector("#request-panel:not([hidden])", { visible: true });
};

const projects = async (page) => {
  await page.waitForSelector('[data-inbox-mode="projects"]', { visible: true });
  await page.click('[data-inbox-mode="projects"]');
  await page.waitForSelector("[data-project-new]", { visible: true });
};

const opened = (trigger, shown) => async (page) => {
  await trigger(page);
  await page.waitForSelector(shown, { visible: true });
};

export const VIEWS = [
  { name: "slash menu", dashboard: "full", hash: "conversations", act: opened(async (page) => (await openSession(page), await page.type("#message", "/")), "#slash-menu") },
  { name: "toast", dashboard: "full", hash: "overview", ready: "#recent-activity > *", act: opened((page) => page.evaluate(() => toast("Sent to review. Its reply appears in Conversations.")), "#toast") },
  { name: "voice dialog", dashboard: "full", hash: "overview", act: opened((page) => page.click("#voice-orchestrator"), "#voice-dialog[open]") },
  { name: "project dialog", dashboard: "full", hash: "conversations", act: opened(async (page) => (await projects(page), await page.click("[data-project-new]")), "#project-dialog[open]") },
  { name: "project menu", dashboard: "full", hash: "conversations", act: opened(async (page) => (await projects(page), await page.click(".project-menu summary")), ".project-menu[open] .project-menu-items") },
  { name: "host picker", dashboard: "full", hash: "conversations", act: opened(async (page) => (await page.click("#new-conversation-toggle"), await page.waitForSelector("#new-conversation-host", { visible: true }), await page.click("#new-conversation-host")), "#new-conversation-host-menu") },
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

export async function openView(page, dashboard, view) {
  await page.goto("about:blank");
  await page.goto(`${dashboard.url}/#${view.hash}`, { waitUntil: "load" });
  await page.waitForFunction(() => document.querySelector("#daemon-status")?.textContent !== "Connecting");
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
