import { app } from "./state.js";
import { $ } from "./dom.js";
import { clearLogicalRequest } from "./actions.js";
import { reloadStartCatalog } from "./start-catalog.js";
import { escape } from "./format.js";

export const hostStateLabels = { connecting: "Connecting", unreachable: "Unreachable", unauthorized: "Pairing revoked", incompatible: "Needs update" };

export const hostIcon = '<svg aria-hidden="true" width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="1.5" y="2.5" width="13" height="8.5" rx="1.5"/><path d="M5.5 13.5h5M8 11v2.5"/></svg>';

export function startHosts() {
  const local = app.state.daemon?.host;
  const paired = app.state.daemon?.hosts || [];
  if (!local || paired.length === 0) return [];
  return [{ id: "", name: local.name, local: true, state: "connected" }, ...paired.map((host) => ({ id: host.id, name: host.name, state: host.state }))];
}

export function defaultStartHost() {
  const host = app.selected?.host;
  if (!host || host.state === "local") return "";
  return (app.state.daemon?.hosts || []).some((paired) => paired.id === host.id && paired.state === "connected") ? host.id : "";
}

export function startHostQuery(prefix) {
  return app.startHost ? `${prefix}host=${encodeURIComponent(app.startHost)}` : "";
}

export function renderStartHost() {
  const button = $("#new-conversation-host");
  const menu = $("#new-conversation-host-menu");
  const hosts = startHosts();
  if (app.startHost && !hosts.some((host) => host.id === app.startHost)) {
    app.startHost = "";
    clearLogicalRequest("session-create");
    if (!$("#new-conversation-form").hidden) {
      reloadStartCatalog();
      return;
    }
  }
  button.hidden = hosts.length === 0;
  const submit = $("#new-conversation-form").querySelector('button[type="submit"]');
  if (hosts.length === 0) {
    menu.hidden = true;
    submit.disabled = false;
    return;
  }
  const current = hosts.find((host) => host.id === app.startHost) || hosts[0];
  submit.disabled = current.state !== "connected";
  button.innerHTML = `${hostIcon}<span>${escape(current.name)}</span>`;
  button.title = `Starts on ${current.name}`;
  button.setAttribute("aria-label", `Starts on ${current.name}. Choose where to start`);
  button.setAttribute("aria-expanded", String(!menu.hidden));
  menu.innerHTML = hosts.map((host) => {
    const ready = host.state === "connected";
    return `<button type="button" role="option" data-start-host="${escape(host.id)}" aria-selected="${host.id === app.startHost}"${ready ? "" : " disabled"}><span>${escape(host.name)}${host.local ? " · this machine" : ""}</span>${ready ? "" : `<small>${escape(hostStateLabels[host.state] || host.state)}</small>`}</button>`;
  }).join("");
}
