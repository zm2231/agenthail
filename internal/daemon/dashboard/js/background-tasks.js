import { $ } from "./dom.js";
import { escape } from "./format.js";

export const backgroundKinds = { shell: "Shell", agent: "Agent", monitor: "Monitor" };

export const backgroundSources = { host: "", transcript: "Read from the transcript.", status: "Claude reports a background shell but not which one." };

export function renderBackgroundTasks(session) {
  const panel = $("#background-panel");
  const background = session?.backgroundTasks;
  if (!background || (!background.running && !(background.tasks || []).length)) {
    panel.innerHTML = "";
    panel.hidden = true;
    return;
  }
  const tasks = (background.tasks || []).map((task) => {
    const kind = backgroundKinds[task.kind] || task.kind || "Task";
    const since = task.since ? ` · since <time datetime="${escape(task.since)}">${escape(new Date(task.since).toLocaleTimeString())}</time>` : "";
    const ambient = task.ambient ? ' <span class="background-ambient">ambient</span>' : "";
    return `<li data-background-task="${escape(task.id || "")}"><span class="background-kind">${escape(kind)}</span> ${escape(task.description || task.id || "")}${ambient}${since}</li>`;
  });
  const running = background.tasks?.filter((task) => !task.ambient).length || 0;
  const title = !background.running ? "Only ambient tasks run" : running ? `${running} background task${running === 1 ? "" : "s"} running` : "Background work running";
  const source = backgroundSources[background.source] || "";
  panel.innerHTML = `<div class="background-card"><h3>${escape(title)}</h3>${tasks.length ? `<ul>${tasks.join("")}</ul>` : ""}${source ? `<p>${escape(source)}</p>` : ""}</div>`;
  panel.hidden = false;
}
