import { app } from "./state.js";
import { escape, labels } from "./format.js";
import { checkoutHost } from "./project-model.js";
import { $ } from "./dom.js";
import { mobileConversationView } from "./navigation.js";
import { renderSlashMenu, syncComposerAction } from "./composer.js";
import { syncRequests } from "./requests.js";
import { renderBackgroundTasks } from "./background-tasks.js";
import { conversationMeta } from "./session-labels.js";
import { renderContextUsage } from "./context-usage.js";
import { renderLiveTurn, startLiveStream } from "./session-stream.js";
import { goalMetrics, renderGoalAttention } from "./goals.js";
import { remoteHostName } from "./hosts.js";
import { renderClaudeMetadata } from "./claude-metadata.js";
import { renderTimeline, timelineSignature } from "./timeline.js";
import { exchangePrompt, renderMessage } from "./messages.js";
import { alignTranscriptTop } from "./transcript-scroll.js";
import { expandAgentsPreference, markClampedAgentRows } from "./agent-messages.js";
import { limitMoveButton } from "./limit-move.js";

export function renderChat() {
  const { exchanges = [], goal, model, models = [], capabilities = {}, readOnly, readOnlyReason, context, transcriptWarning, claudeRuns, claudeSubagents, claudeSubagentsTotal } = app.history || {};
  const session = app.selected;
  const controls = [];
  if (session.runtime?.focusable)
    controls.push('<button data-action="session-focus" type="button">Open session</button>');
  if (session.status === "busy" && !session.waitingOn && capabilities.steer)
    controls.push('<button data-action="steer" type="button">Steer</button>');
  if (session.status === "busy" && capabilities.interrupt)
    controls.push(
      '<button data-action="interrupt" type="button">Stop</button>',
    );
  if (capabilities.compact)
    controls.push(
      '<button data-action="compact" type="button">Compact</button>',
    );
  if (session.limitResume) {
    controls.push('<button data-action="limit-resume" type="button">Resume now</button>');
    if (session.limitResume.state === "waiting")
      controls.push('<button data-action="limit-resume-cancel" type="button">Don\'t resume</button>');
    if (session.limitResume.moveTo)
      controls.push(limitMoveButton(session.limitResume.moveTo));
  }
  const expandAgents = expandAgentsPreference();
  controls.push(`<button data-expand-agents aria-pressed="${expandAgents}" type="button">${expandAgents ? "Collapse agent messages" : "Expand agent messages"}</button>`);
  controls.push(`<button data-archive-action="${session.archived ? "session-unarchive" : "session-archive"}" type="button">${session.archived ? "Unarchive" : "Archive"}</button>`);
  if (session.cwd && !(session.project?.added && session.project.rootPath === session.cwd))
    controls.push(`<button data-project-add="${escape(session.cwd)}" data-project-host="${escape(checkoutHost(session))}" type="button">Add folder as project</button>`);
  if (session.project && !session.subagent && (session.surface === "claude" || session.surface === "codex") && !(app.state.projects || []).some((project) => project.id === session.project.id && (project.coordinators || []).some((lead) => lead.sessionId === session.id)))
    controls.push('<button data-make-lead type="button">Make project lead</button>');
  $("#chat-actions").innerHTML = mobileConversationView()
    ? ""
    : controls.join("");
  app.slashCommands = [
    ["/name", "Name this conversation"],
    capabilities.compact && ["/compact", "Compact this conversation"],
    session.status === "busy" && capabilities.interrupt && ["/stop", "Stop the current turn"],
    session.status === "busy" && !session.waitingOn && capabilities.steer && ["/steer", "Redirect the current turn"],
    capabilities.model && ["/model", "Choose the active model"],
    capabilities.goalSet && ["/goal", "Set the session goal"],
  ].filter(Boolean);
  if (readOnly) {
    $("#message").disabled = true;
    $("#send").disabled = true;
    $("#message").placeholder = "Read only";
    $("#composer-note").textContent = readOnlyReason;
  }
  syncComposerAction();
  renderSlashMenu();
  syncRequests();
  renderBackgroundTasks(session);
  $("#chat-subtitle").textContent = conversationMeta(session, model, app.history?.pendingModel ?? session.pendingModel ?? "");
  $("#thread-count").textContent =
    `${exchanges.length} recent exchange${exchanges.length === 1 ? "" : "s"}`;
  renderContextUsage(context);
  startLiveStream();
  const settings = [`<form class="session-tools" data-tool="alias"><label><span>Conversation name</span><input name="alias" value="${escape(session.alias || "")}" maxlength="80" placeholder="research"></label><div class="session-tool-actions"><button class="soft-button" type="submit">Save name</button></div></form>`];
  if (capabilities.goalSet) {
    const goalActions = [];
    if (capabilities.goal && goal?.status === "active") goalActions.push('<button class="soft-button" data-action="goal-pause" type="button">Pause</button>');
    if (capabilities.goal && goal?.status === "paused") goalActions.push('<button class="soft-button" data-action="goal-resume" type="button">Resume</button>');
    if (goal?.objective) goalActions.push('<button class="soft-button" data-action="goal-clear" type="button">Clear</button>');
    if (capabilities.goal && goal?.tokenBudget != null) goalActions.push('<button class="soft-button" data-action="goal-budget" data-goal-budget-clear="true" type="button">Clear budget</button>');
    const metrics = goalMetrics(goal);
    settings.push(`<form class="session-tools" data-tool="goal"><label><span>Goal</span><input name="goal" value="${escape(goal?.objective || "")}" placeholder="Set a focused goal"></label>${metrics}<div class="session-tool-actions"><button class="soft-button" type="submit">${goal?.objective ? "Edit goal" : "Save goal"}</button>${goalActions.join("")}</div></form>`);
    if (capabilities.goal) settings.push(`<form class="session-tools" data-tool="goal-budget"><label><span>Token budget</span><input name="tokenBudget" type="number" min="0" step="1" value="${goal?.tokenBudget ?? ""}" placeholder="Optional"></label><div class="session-tool-actions"><button class="soft-button" type="submit">Save budget</button></div></form>`);
  } else if (capabilities.goalStatus && goal?.objective) {
    settings.push(`<div class="session-tools session-goal-status" data-tool="goal-status"><label><span>Goal</span><input value="${escape(goal.objective)}" readonly></label>${goalMetrics(goal)}</div>`);
  }
  const toolRows = [
    `<details class="session-details"><summary>Conversation settings</summary>${settings.join("")}</details>`,
    remoteHostName(session) ? "" : `<details class="session-details"><summary>Voice</summary><p>Call this exact conversation through Codex Voice, or transfer an active call here.</p><button class="soft-button" type="button" data-voice-session="${escape(session.id)}">Call this session</button></details>`,
  ];
  if (session.surface === "claude") toolRows.push(renderClaudeMetadata(claudeRuns, claudeSubagents, claudeSubagentsTotal, app.allSubagents));
  const timeline = app.history?.timeline?.items || [];
  const signature = timelineSignature(session, exchanges, goal, capabilities, transcriptWarning, timeline, claudeRuns, claudeSubagents, claudeSubagentsTotal, app.allSubagents, expandAgents);
  if (app.transcriptSignature === signature) return;
  const chatBody = $("#chat-body");
  const previousScrollTop = chatBody.scrollTop;
  const wasPinned =
    chatBody.scrollHeight - chatBody.scrollTop - chatBody.clientHeight <= 24;
  const messages = timeline.length
    ? renderTimeline(timeline, session, expandAgents)
    : exchanges
    .flatMap((exchange, index) => {
      const user = exchangePrompt(exchange);
      const assistant = String(exchange.assistant || "");
      return [
        exchange.user
          ? renderMessage(
              user.text,
              "user",
              user.label,
              `${session.id}:user:${index}`,
            )
          : "",
        assistant
          ? renderMessage(
              assistant,
              "agent",
              labels[session.surface] || session.surface,
              `${session.id}:agent:${index}`,
            )
          : "",
      ];
    })
    .join("");
  chatBody.innerHTML =
    `${transcriptWarning ? `<p role="status">${escape(transcriptWarning)}</p>` : ""}${renderGoalAttention(goal)}${toolRows.join("")}${messages || '<div class="empty-state"><span class="empty-glyph">✦</span><h2>No saved exchanges yet</h2><p>Send a message to start this conversation from Agenthail.</p></div>'}`;
  markClampedAgentRows(chatBody);
  renderLiveTurn();
  app.transcriptSignature = signature;
  if (app.pendingEntryScroll) {
    const turns = chatBody.querySelectorAll(".turn");
    const latest = turns[turns.length - 1];
    chatBody.scrollTop = latest
      ? chatBody.scrollTop + latest.getBoundingClientRect().top - chatBody.getBoundingClientRect().top
      : 0;
    alignTranscriptTop(chatBody);
    app.pendingEntryScroll = false;
  } else {
    chatBody.scrollTop = wasPinned
      ? chatBody.scrollHeight
      : Math.min(previousScrollTop, chatBody.scrollHeight - chatBody.clientHeight);
  }
}
