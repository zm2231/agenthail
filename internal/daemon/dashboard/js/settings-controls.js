import { action } from "./actions.js";
import { load } from "./dashboard-state.js";
import { friendlyError, toast } from "./feedback.js";
import { createLoginLink, hideRemoteQR, revealRemoteQR, updateSettings } from "./settings.js";
import { $ } from "./dom.js";

export function bindSettingsEvents() {
  document.addEventListener("click", async (event) => {
    const repair = event.target.closest("[data-surface-repair]");
    if (repair) {
      repair.disabled = true;
      try {
        await action(repair.dataset.surfaceRepair);
        await load(true);
        toast("Surface repaired.");
      } catch (error) {
        toast(friendlyError(error));
      } finally {
        repair.disabled = false;
      }
      return;
    }
    if (event.target.closest("[data-reveal-remote-qr]")) {
      revealRemoteQR();
      return;
    }
    if (event.target.closest("[data-hide-remote-qr]")) {
      hideRemoteQR();
      return;
    }
    const remote = event.target.closest("[data-remote-action]");
    if (remote) {
      remote.disabled = true;
      try {
        await updateSettings({ action: remote.dataset.remoteAction });
        toast(remote.dataset.remoteAction === "remote-enable" ? "Phone access enabled." : "Phone access disabled.");
      } catch (error) {
        toast(error.message);
      }
      return;
    }
    if (event.target.closest("[data-copy-remote]")) {
      let link;
      try {
        link = await createLoginLink();
      } catch (error) {
        toast(error.message);
        return;
      }
      try {
        if (!navigator.clipboard) throw Error("Clipboard access is unavailable");
        await navigator.clipboard.writeText(link);
        toast("Phone link copied. It works once, within 15 minutes.");
      } catch {
        window.prompt("Copy this phone link. It works once, within 15 minutes.", link);
      }
    }
  });
  window.addEventListener("blur", hideRemoteQR);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) hideRemoteQR();
  });
  $("#dashboard-settings-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await updateSettings({ action: "dashboard-config", codexRecentHours: Number($("#settings-codex-hours").value), busyDelivery: $("#settings-busy-delivery").value });
      await load(true);
      toast("Settings saved.");
    } catch (error) {
      toast(error.message);
    }
  });
  $("#claude-accounts-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await updateSettings({ action: "claude-accounts", claudeAccounts: {
        auto: $("#settings-claude-auto").value === "on",
        preferred: $("#settings-claude-preferred").value,
        sessionCeiling: Number($("#settings-claude-session").value),
        weeklyCeiling: Number($("#settings-claude-weekly").value),
        autoMove: $("#settings-claude-automove").value === "on",
      } });
      await load(true);
      toast("New Claude sessions follow the saved account choice.");
    } catch (error) {
      toast(error.message);
    }
  });
  $("#settings-resume-after-limit").addEventListener("change", async (event) => {
    try {
      await updateSettings({ action: "resume-after-limit", resumeAfterLimit: event.target.checked });
      toast(event.target.checked ? "Stopped sessions resume after their usage limit resets." : "Stopped sessions are no longer resumed.");
    } catch (error) {
      event.target.checked = !event.target.checked;
      toast(error.message);
    }
  });
}
