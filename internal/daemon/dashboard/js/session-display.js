export function requireDisplay(session) {
  const display = session?.display;
  if (!display || !["kind", "label", "detail"].every((key) => typeof display[key] === "string")) {
    throw Error("The daemon did not send session presentation. Update Agenthail.");
  }
  for (const key of ["resetsAt", "resumesAt", "quietSince", "toolStartedAt"]) {
    if (display[key] !== undefined && !Number.isFinite(Date.parse(display[key]))) throw Error(`Invalid display instant: ${key}`);
  }
  return display;
}

export function displayText(session) {
  const display = requireDisplay(session);
  const parts = [display.label, display.detail];
  for (const key of ["resetsAt", "resumesAt", "quietSince", "toolStartedAt"]) {
    if (display[key]) parts.push(new Date(display[key]).toLocaleString());
  }
  return parts.filter(Boolean).join(" · ");
}
