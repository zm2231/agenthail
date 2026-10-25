import { shortHostName } from "./hosts.js";
import { escape } from "./format.js";

export function checkoutHost(checkout) {
  return checkout?.host && checkout.host.state !== "local" ? checkout.host.id : "";
}

export function projectFolderSuggestions(options) {
  const roots = (options?.projects || []).filter((project) => !project.added).map((project) => project.rootPath);
  return [...new Set([...roots, ...(options?.folders || [])])];
}

export function leadRequest(checkout, values, replace) {
  const request = { surface: values.surface, cwd: checkout.rootPath };
  const host = checkoutHost(checkout);
  if (host) request.host = host;
  if (values.name.trim()) request.name = values.name.trim();
  if (replace) request.replace = true;
  return request;
}

export function coordinatorRef(coordinator) {
  const name = `@${coordinator.name}`;
  return coordinator.host && coordinator.host.state !== "local" ? `${shortHostName(coordinator.host.name)}::${name}` : name;
}

export function coordinatorChip(coordinators) {
  if (coordinators.length === 0) return "";
  if (coordinators.length > 1) {
    const refs = coordinators.map(coordinatorRef).join(", ");
    return `<span class="lead-chip conflict" title="${escape(`Several hosts each have a coordinator: ${refs}. Change the lead to keep one.`)}">${coordinators.length} leads</span>`;
  }
  const ref = coordinatorRef(coordinators[0]);
  if (!coordinators[0].sessionId) return `<span class="lead-chip idle" title="${escape(`${ref} starts on its first message`)}">${escape(ref)}</span>`;
  return `<button class="lead-chip" type="button" data-session="${escape(coordinators[0].sessionId)}" title="${escape(`Open ${ref}, the project coordinator`)}">${escape(ref)}</button>`;
}
