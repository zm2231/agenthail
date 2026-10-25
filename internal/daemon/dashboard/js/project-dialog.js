import { toast } from "./feedback.js";
import { load } from "./dashboard-state.js";
import { $ } from "./dom.js";
import { app } from "./state.js";
import { defaultStartHost, startHosts } from "./start-host.js";
import { escape } from "./format.js";
import { coordinatorRef, leadRequest, projectFolderSuggestions } from "./project-model.js";
import { action } from "./actions.js";

export async function projectChange(control, change) {
  control.disabled = true;
  try {
    await change();
  } catch (error) {
    toast(error.message);
  } finally {
    control.disabled = false;
    await load();
  }
}

export async function changeLead(send) {
  try {
    return await send(false);
  } catch (error) {
    if (error.code !== "coordinator_exists" && error.code !== "coordinator_conflict") throw error;
    if (!window.confirm(`${error.message}. Replace it?`)) return null;
    return await send(true);
  }
}

export const projectDialogFields = { add: ["host", "folder", "title"], rename: ["title"], lead: ["checkout", "surface", "name"] };

export function openProjectDialog(mode, project) {
  const form = $("#project-form");
  form.reset();
  app.projectDialog = { mode, project };
  $("#project-dialog-title").textContent = { add: "Add project", rename: `Rename ${project?.displayName}`, lead: `Lead of ${project?.displayName}` }[mode];
  $("#project-dialog-error").hidden = true;
  const help = $("#project-dialog-help");
  help.hidden = true;
  const hosts = startHosts();
  const fields = projectDialogFields[mode].filter((field) => field !== "host" || hosts.length > 0);
  form.querySelectorAll("[data-project-field]").forEach((field) => {
    const shown = fields.includes(field.dataset.projectField);
    field.hidden = !shown;
    field.querySelectorAll("input,select").forEach((input) => (input.disabled = !shown));
  });
  form.elements.title.required = mode === "rename";
  if (mode === "add") {
    form.elements.host.innerHTML = hosts.map((host) => `<option value="${escape(host.id)}"${host.state === "connected" ? "" : " disabled"}>${escape(host.name)}${host.local ? " · this machine" : ""}</option>`).join("");
    form.elements.host.value = defaultStartHost();
    loadProjectFolders();
  } else if (mode === "rename") {
    form.elements.title.value = project.displayName;
    if ((project.checkouts || []).some((checkout) => !checkout.added)) {
      help.textContent = "The name applies on every host where this project was added.";
      help.hidden = false;
    }
  } else {
    const usable = (project.checkouts || []).map((checkout, index) => ({ checkout, index })).filter(({ checkout }) => !checkout.archived && ["local", "connected"].includes(checkout.host?.state));
    form.elements.checkout.innerHTML = usable.map(({ checkout, index }) => `<option value="${index}">${escape(`${checkout.host?.name || "This machine"}: ${checkout.rootPath}`)}</option>`).join("");
    const leads = project.coordinators || [];
    if (leads.length) {
      help.textContent = `Starts a new lead conversation in place of ${leads.map(coordinatorRef).join(" and ")}. To make an existing conversation the lead, open it and choose Make project lead.`;
      help.hidden = false;
    }
  }
  $("#project-dialog").showModal();
}

export async function loadProjectFolders() {
  const form = $("#project-form");
  const host = form.elements.host.value;
  const list = $("#project-folders");
  list.innerHTML = "";
  try {
    const response = await fetch(`/api/v1/session-options${host ? `?host=${encodeURIComponent(host)}` : ""}`);
    if (!response.ok || form.elements.host.value !== host) return;
    list.innerHTML = projectFolderSuggestions(await response.json()).map((folder) => `<option value="${escape(folder)}"></option>`).join("");
  } catch {}
}

export function bindProjectEvents() {
  $("#project-form").elements.host.addEventListener("change", loadProjectFolders);
  $("#project-dialog-close").addEventListener("click", () => $("#project-dialog").close());
  $("#project-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = event.target;
    const { mode, project } = app.projectDialog || {};
    const submit = form.querySelector('button[type="submit"]');
    const failure = $("#project-dialog-error");
    submit.disabled = true;
    failure.hidden = true;
    try {
      const title = form.elements.title.value.trim();
      if (mode === "add") {
        const host = form.elements.host.disabled ? "" : form.elements.host.value;
        const result = await action("project-add", { path: form.elements.path.value.trim(), ...(title ? { title } : {}), ...(host ? { host } : {}) }, crypto.randomUUID(), "");
        toast(result.created === false ? `${result.project.displayName} is already a project.` : `Added project ${result.project.displayName}.`);
      } else if (mode === "rename") {
        await action("project-rename", { projectId: project.id, title }, crypto.randomUUID(), "");
        toast(`Renamed to ${title}.`);
      } else if (mode === "lead") {
        const checkout = project.checkouts[Number(form.elements.checkout.value)];
        if (!checkout) throw Error("No host of this project is connected.");
        const values = { surface: form.elements.surface.value, name: form.elements.name.value };
        const result = await changeLead((replace) => action("coordinator-new", leadRequest(checkout, values, replace), crypto.randomUUID(), ""));
        if (!result) return;
        toast(`@${result.coordinator.name} leads ${project.displayName}; its first message starts it.`);
      }
      $("#project-dialog").close();
      await load();
    } catch (error) {
      failure.textContent = error.message;
      failure.hidden = false;
    } finally {
      submit.disabled = false;
    }
  });
}
