import { escape } from "./format.js";

export function statusPill(status) {
  return `<span class="status-pill ${escape(status)}">${escape(statusLabel(status))}</span>`;
}

export function statusLabel(status) {
  return (
    {
      busy: "Working",
      idle: "Ready",
      offline: "Offline",
      unknown: "Unavailable",
      notLoaded: "Unavailable",
    }[status] || status
  );
}

export function backgroundWaiting(session) {
  return session.status !== "busy" && Boolean(session.backgroundTasks?.running);
}

export function waitingPlace(session) {
  const waiting = session?.waitingOn;
  if (!waiting) return "";
  if (waiting.answerable) return "agenthail";
  if (waiting.kind === "dialog") return "dialog";
  const lifecycle = session.capabilities?.lifecycle || {};
  if (lifecycle.stop && lifecycle.resume) return "restart";
  if (session.runtimeKind === "claude-cli") return "terminal";
  return "elsewhere";
}

export const waitingLabels = {
  agenthail: "Waiting on you",
  dialog: "Dialog open in its terminal",
  terminal: "Waiting in its terminal",
  restart: "Prompt out of reach",
  elsewhere: "Waiting on you",
};

export function usageLimitLabel(session) {
  const resets = Date.parse(session.usageLimit?.resetsAt || "");
  return resets ? `Usage limit, resets ${new Date(resets).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}` : "Usage limit reached";
}

export function limitResumeLabel(session) {
  const stop = session.limitResume;
  if (stop.state !== "waiting") return "Stopped by usage limit";
  const resets = Date.parse(stop.resetsAt || "");
  if (!resets) return "Usage limit, resumes when it lifts";
  const at = new Date(resets);
  const sameDay = at.toDateString() === new Date().toDateString();
  return `Usage limit, resumes ${at.toLocaleString([], sameDay ? { hour: "numeric", minute: "2-digit" } : { weekday: "short", hour: "numeric", minute: "2-digit" })}`;
}

export function limitLabel(session) {
  return session.limitResume ? limitResumeLabel(session) : session.usageLimit ? usageLimitLabel(session) : "";
}

export const quietAfterMs = 10 * 60 * 1000;

export function quietNote(activity, now = Date.now()) {
  const last = Date.parse(activity?.lastProgressAt || activity?.busySince || "");
  if (!last || now - last < quietAfterMs) return "";
  const parts = [`no activity for ${quietDuration(now - last)}`];
  if (activity.noProcess) {
    parts.push("no Claude process");
  } else if (activity.tool?.name) {
    const started = Date.parse(activity.tool.startedAt || "");
    parts.push(`running ${activity.tool.name}${started ? ` since ${new Date(started).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}` : ""}`);
  }
  return parts.join(" · ");
}

export function quietDuration(ms) {
  const minutes = Math.floor(ms / 60000);
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  return minutes % 60 ? `${hours} hr ${minutes % 60} min` : `${hours} hr`;
}

export function workingLabel(session, now = Date.now()) {
  const note = quietNote(session.activity, now);
  return note ? `Working · ${note}` : "Working";
}

export function livePlan(session) {
  const plan = session.plan;
  if (!session.open || session.status === "offline" || session.unavailableReason || !plan?.total || plan.done >= plan.total) return null;
  return plan;
}

export function planDetail(plan) {
  const done = `${plan.done} of ${plan.total} tasks done`;
  return plan.current ? `${done} · ${plan.current}` : done;
}

export function presenceLabel(session, now = Date.now()) {
  if (session.waitingOn) return waitingLabels[waitingPlace(session)];
  if (limitLabel(session)) return limitLabel(session);
  if (backgroundWaiting(session)) return "Waiting on background work";
  if (session.status === "busy") return workingLabel(session, now);
  return (
    {
      working: "Working",
      queued: "Queued",
      open: "Open",
      recent: "Recent",
    }[session.currentReason] || statusLabel(session.status)
  );
}

export function presenceTone(session) {
  if (backgroundWaiting(session)) return "background";
  if (session.status === "busy" || ["working", "open"].includes(session.currentReason)) return "active";
  if (session.status === "idle" || ["queued", "blocked"].includes(session.currentReason)) return "idle";
  return session.status;
}
