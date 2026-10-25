import { showToolDiff, toggleFileChip } from "./tool-diffs.js";
import { collapseToolRow, showToolOutput } from "./tool-view.js";
import { friendlyError, toast } from "./feedback.js";
import { $ } from "./dom.js";
import { renderSlashMenu, resizeComposer, syncComposerAction } from "./composer.js";
import { mobileConversationView, showView, syncConversationLayout } from "./navigation.js";
import { app } from "./state.js";
import { renderSessions, sessionBatchSize } from "./sessions.js";
import { selectSession } from "./session-selection.js";
import { openVoice } from "./voice.js";
import { cancelCodexSearch } from "./session-search.js";
import { action } from "./actions.js";
import { renderDeliveryProblems } from "./overview.js";
import { load } from "./dashboard-state.js";
import { displayName } from "./session-labels.js";
import { changeLead, openProjectDialog, projectChange } from "./project-dialog.js";
import { toggleNewConversationForm } from "./start-form.js";
import { checkoutHost } from "./project-model.js";
import { loadSessionMetadata } from "./session-metadata.js";
import { renderContextUsage } from "./context-usage.js";

export function bindConversationEvents() {
  document.addEventListener("click", async (event) => {
    const chip = event.target.closest("[data-chip]");
    if (chip) return toggleFileChip(chip);
    const diffShow = event.target.closest("[data-diff-show]");
    if (diffShow) return showToolDiff(diffShow);
    const outputShow = event.target.closest("[data-output-show]");
    if (outputShow) return showToolOutput(outputShow);
    const collapseRow = event.target.closest("[data-collapse-row]");
    if (collapseRow) return collapseToolRow(collapseRow);
    const copyOutput = event.target.closest("[data-copy-text]");
    if (copyOutput) {
      try {
        await navigator.clipboard.writeText(copyOutput.dataset.copyText);
        toast(copyOutput.dataset.copyDone || "Copied.");
      } catch (error) {
        toast("Couldn't copy.");
      }
      return;
    }
    const editMessage = event.target.closest("[data-edit-message]");
    if (editMessage) {
      const composer = $("#message");
      if (composer.disabled) {
        toast("This session is read only.");
        return;
      }
      composer.value = composer.value.trim() ? `${editMessage.dataset.editMessage}\n\n${composer.value}` : editMessage.dataset.editMessage;
      renderSlashMenu();
      resizeComposer();
      syncComposerAction();
      composer.focus();
      return;
    }
    const modelOption = event.target.closest("[data-model-option]");
    if (modelOption) {
      $("#message").value = `/model ${modelOption.dataset.modelOption}`;
      renderSlashMenu();
      resizeComposer();
      $("#message").focus();
      return;
    }
    const slash = event.target.closest("[data-slash]");
    if (slash) {
      const command = slash.dataset.slash;
      $("#message").value = command + (["/steer", "/model", "/goal", "/name"].includes(command) ? " " : "");
      renderSlashMenu();
      resizeComposer();
      $("#message").focus();
      return;
    }
    const viewLink = event.target.closest("[data-view]");
    if (viewLink?.dataset.view === "conversations" && mobileConversationView()) {
      app.mobileChatOpen = false;
      syncConversationLayout();
    }
    const mode = event.target.closest("[data-inbox-mode]");
    if (mode) {
      app.inboxMode = mode.dataset.inboxMode;
      if (app.inboxMode === "current") {
        app.filters.surface = "all";
        app.filters.status = "all";
        $("#session-surface-filter").value = "all";
        $("#session-status-filter").value = "all";
      }
      app.sessionLimit = sessionBatchSize();
      renderSessions();
      return;
    }
    const projectExpand = event.target.closest("[data-project-expand]");
    if (projectExpand) {
      const id = projectExpand.dataset.projectExpand;
      if (app.expandedProjects.has(id)) app.expandedProjects.delete(id);
      else app.expandedProjects.add(id);
      renderSessions();
      return;
    }
    const archivedProjects = event.target.closest(".archived-projects > summary");
    if (archivedProjects) {
      event.preventDefault();
      app.openArchivedProjects = !app.openArchivedProjects;
      renderSessions();
      return;
    }
    const sessionTarget = event.target.closest("[data-session]");
    if (sessionTarget) return selectSession(sessionTarget.dataset.session, true);
    const voiceSession = event.target.closest("[data-voice-session]");
    if (voiceSession) return openVoice(voiceSession.dataset.voiceSession);
    const surfaceButton = event.target.closest("[data-surface]");
    if (surfaceButton) {
      app.mobileChatOpen = false;
      showView("conversations");
      $("#session-search").value = "";
      cancelCodexSearch();
      app.codexSearch = { query: "", results: [], loading: false, error: "", timer: null, controller: null };
      app.inboxMode = "all";
      app.sessionLimit = sessionBatchSize();
      app.filters.surface = surfaceButton.dataset.surface;
      $("#session-surface-filter").value = app.filters.surface;
      renderSessions();
      return;
    }
    if (event.target.closest("[data-open-conversations]")) {
      app.mobileChatOpen = false;
      return showView("conversations");
    }
    if (event.target.closest("[data-open-operations]"))
      return showView("operations");
    const retry = event.target.closest("[data-retry]");
    const deliveryDismiss = event.target.closest("[data-delivery-dismiss]");
    if (deliveryDismiss) {
      deliveryDismiss.disabled = true;
      try {
        const deliveryId = Number(deliveryDismiss.dataset.deliveryDismiss);
        await action("delivery-dismiss", { deliveryId });
        app.state.deliveryProblems = app.state.deliveryProblems.filter(item => item.deliveryId !== deliveryId);
        renderDeliveryProblems();
      } catch (error) {
        deliveryDismiss.disabled = false;
        toast(friendlyError(error));
      }
      return;
    }
    if (retry) {
      retry.disabled = true;
      try {
        await action("queue-retry", { queueId: Number(retry.dataset.retry) });
        toast("Queued message scheduled for retry.");
        await load();
      } catch (error) {
        toast(error.message);
      }
      return;
    }
    const cancel = event.target.closest("[data-cancel]");
    if (cancel) {
      cancel.disabled = true;
      try {
        await action("queue-cancel", { queueId: Number(cancel.dataset.cancel) });
        toast("Queued message canceled.");
        await load();
      } catch (error) {
        toast(error.message);
      }
      return;
    }
    const projectMessage = event.target.closest("[data-project-message]");
    if (projectMessage) {
      const message = window.prompt(`Message every active conversation in ${projectMessage.dataset.projectName}`);
      if (!message || !message.trim()) return;
      projectMessage.disabled = true;
      try {
        const result = await action("channel-send", { channel: `#${projectMessage.dataset.projectMessage}`, message, author: "person" }, crypto.randomUUID(), "");
        const names = (result.recipients || []).map((recipient) => recipient.coordinator || displayName(app.state.sessions.find((session) => session.id === recipient.sessionId) || { id: recipient.sessionId }));
        toast(`Sent to ${names.length}: ${names.join(", ")}.`);
      } catch (error) {
        toast(error.message);
      } finally {
        projectMessage.disabled = false;
        await load();
      }
      return;
    }
    const projectControl = event.target.closest("[data-project-new], [data-project-rename], [data-project-start], [data-project-lead], [data-project-clear-lead], [data-make-lead]");
    if (projectControl) {
      projectControl.closest("details.project-menu")?.removeAttribute("open");
      const project = (app.state.projects || []).find((item) => item.id === (projectControl.dataset.projectRename || projectControl.dataset.projectStart || projectControl.dataset.projectLead || projectControl.dataset.projectClearLead));
      if (projectControl.dataset.projectNew !== undefined) openProjectDialog("add");
      else if (projectControl.dataset.projectRename && project) openProjectDialog("rename", project);
      else if (projectControl.dataset.projectLead && project) openProjectDialog("lead", project);
      else if (projectControl.dataset.projectStart && project) {
        const checkout = project.checkouts[Number(projectControl.dataset.checkout)];
        if (checkout) toggleNewConversationForm(true, { host: checkoutHost(checkout), folder: checkout.rootPath });
      } else if (projectControl.dataset.projectClearLead && project) {
        if (!window.confirm(`Clear the lead of ${project.displayName}? Its conversation and name stay.`)) return;
        await projectChange(projectControl, async () => {
          await action("coordinator-clear", { projectId: project.id }, crypto.randomUUID(), "");
          toast(`${project.displayName} has no lead now.`);
        });
      } else if (projectControl.dataset.makeLead !== undefined && app.selected) {
        const sessionID = app.selected.id;
        await projectChange(projectControl, async () => {
          const result = await changeLead((replace) => action("coordinator-set", replace ? { replace: true } : {}, crypto.randomUUID(), sessionID));
          if (result) toast(`@${result.coordinator.name} leads ${result.coordinator.projectRoot}.`);
        });
      }
      return;
    }
    const viewControl = event.target.closest("[data-archive-action], [data-project-action], [data-project-add]");
    if (viewControl) {
      viewControl.disabled = true;
      try {
        if (viewControl.dataset.archiveAction) {
          await action(viewControl.dataset.archiveAction);
          toast(viewControl.dataset.archiveAction === "session-archive" ? "Conversation archived." : "Conversation unarchived.");
        } else if (viewControl.dataset.projectAction) {
          await action(viewControl.dataset.projectAction, { projectId: viewControl.dataset.projectId }, crypto.randomUUID(), "");
          toast({ "project-archive": "Project archived.", "project-unarchive": "Project unarchived.", "project-remove": "Project removed." }[viewControl.dataset.projectAction]);
        } else {
          const result = await action("project-add", { path: viewControl.dataset.projectAdd, ...(viewControl.dataset.projectHost ? { host: viewControl.dataset.projectHost } : {}) }, crypto.randomUUID(), "");
          toast(result.created === false ? `${result.project?.displayName || "This folder"} is already a project.` : `Added project ${result.project?.displayName || ""}.`);
        }
      } catch (error) {
        toast(error.message);
      } finally {
        viewControl.disabled = false;
        await load();
      }
      return;
    }
    if (event.target.closest("[data-subagents-all]") && app.selected && app.history) {
      app.allSubagents = true;
      loadSessionMetadata(app.selected.id, app.history);
      return;
    }
    const control = event.target.closest("[data-action]");
    if (!control) return;
    control.disabled = true;
    if (control.dataset.action === "session-restart") {
      try {
        await action("session-lifecycle-stop");
        await action("session-lifecycle-resume");
        toast("Stopped and resumed.");
        await load();
        await selectSession(app.selected.id);
      } catch (error) {
        toast(error.message);
      } finally {
        control.disabled = false;
      }
      return;
    }
    try {
      const result = await action(control.dataset.action, {
        message: control.dataset.goalBudgetClear === "true" ? "" : $("#message").value.trim(),
      });
      const queued = result?.result?.evidence === "queued";
      if (control.dataset.action === "compact" && app.history?.context) {
        app.history.context.compacting = true;
        renderContextUsage(app.history.context);
      }
      toast(queued ? `${control.textContent} queued.` : `${control.textContent} requested.`);
      await load();
      await selectSession(app.selected.id);
    } catch (error) {
      toast(error.message);
    } finally {
      control.disabled = false;
    }
  });
}
