export const labels = { claude: "Claude Code", codex: "Codex", notion: "Notion" };

export const escape = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (char) =>
      ({
        "&": "&amp;",
        "<": "&lt;",
        ">": "&gt;",
        '"': "&quot;",
        "'": "&#039;",
      })[char],
  );

export const timeAgo = (value) => {
  const then = new Date(value).getTime();
  if (!then) return "recently";
  const minutes = Math.max(0, Math.round((Date.now() - then) / 60000));
  return minutes < 2
    ? "just now"
    : minutes < 60
      ? `${minutes}m ago`
      : minutes < 1440
        ? `${Math.round(minutes / 60)}h ago`
        : `${Math.round(minutes / 1440)}d ago`;
};

export function surfaceIcon(name) {
  return name === "claude" ? "✦" : name === "codex" ? "◈" : "N";
}

export function clockTime(timestamp) {
  const date = new Date(timestamp || "");
  if (Number.isNaN(date.getTime())) return "";
  const today = new Date().toDateString() === date.toDateString();
  return date.toLocaleString([], today ? { hour: "numeric", minute: "2-digit" } : { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

export function durationClock(value) {
  const text = String(value || "");
  const match = text.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/);
  if (!text || !match) return text;
  const [hours, minutes, seconds] = [match[1], match[2], match[3]].map(part => Number(part || 0));
  const pad = number => String(number).padStart(2, "0");
  return hours ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

export function turnFooter(duration, timestamp) {
  return [duration ? `Finished ${durationClock(duration)}` : "", clockTime(timestamp)].filter(Boolean).join(" · ");
}

export function compactTokenCount(value) {
  const count = Number(value || 0);
  if (count >= 1000000) return `${(count / 1000000).toFixed(count >= 10000000 ? 0 : 1).replace(/\.0$/, "")}m`;
  if (count >= 1000) return `${Math.round(count / 1000)}k`;
  return String(count);
}
