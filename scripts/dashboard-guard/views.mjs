const operations = (tab) => async (page) => {
  await page.click(`[data-operations-tab="${tab}"]`);
};

const openSession = async (page) => {
  await page.waitForSelector("#session-list [data-session]", { visible: true });
  await page.click("#session-list [data-session]");
  await page.waitForFunction(() => document.querySelector("#chat-body")?.textContent.includes("contrast target"));
};

export const VIEWS = [
  { name: "overview", dashboard: "full", hash: "overview", ready: "#recent-activity > *" },
  { name: "conversations", dashboard: "full", hash: "conversations", ready: "#session-list [data-session]" },
  { name: "session open", dashboard: "full", hash: "conversations", act: openSession },
  { name: "operations delivery", dashboard: "full", hash: "operations", ready: "#queue-list > *", act: operations("queue") },
  { name: "operations handoffs", dashboard: "full", hash: "operations", ready: "#relay-list > *", act: operations("network") },
  { name: "operations audit", dashboard: "full", hash: "operations", ready: "#history-list > *", act: operations("audit") },
  { name: "operations settings", dashboard: "full", hash: "operations", act: operations("settings") },
  { name: "empty overview", dashboard: "empty", hash: "overview" },
  { name: "empty conversations", dashboard: "empty", hash: "conversations" },
  { name: "empty operations", dashboard: "empty", hash: "operations", act: operations("queue") },
  { name: "empty audit", dashboard: "empty", hash: "operations", act: operations("audit") },
];

export async function openView(page, dashboard, view) {
  await page.goto(`${dashboard.url}/#${view.hash}`, { waitUntil: "load" });
  await page.waitForFunction(() => document.querySelector("#daemon-status")?.textContent !== "Connecting");
  if (view.act) await view.act(page);
  if (view.ready) await page.waitForSelector(view.ready, { visible: true });
  await settle(page);
}

export async function settle(page) {
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await page.waitForSelector("html[data-ui-settled]");
  await page.evaluate(async () => {
    await document.fonts.ready;
    const finite = () => document.getAnimations().filter((animation) => Number.isFinite(animation.effect?.getComputedTiming().endTime));
    await Promise.all(finite().map((animation) => animation.finished.catch(() => {})));
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
}
