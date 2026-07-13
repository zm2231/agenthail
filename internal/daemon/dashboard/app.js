const $ = (selector) => document.querySelector(selector);
const app = {
  state: {
    sessions: [],
    surfaces: [],
    queue: [],
    channels: [],
    relays: [],
    history: [],
  },
  selected: null,
  history: null,
  filters: { surface: "all", status: "all" },
  inboxMode: "current",
  operationsTab: "queue",
  audit: { items: [], kinds: [], nextBefore: 0, hasMore: true, loaded: false, loading: false, kind: "all", query: "", searchTimer: null },
  sessionLimit: 0,
  sessionViewport: null,
};
const labels = { claude: "Claude Code", codex: "Codex", notion: "Notion" };
globalThis.escape = (value) =>
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
function inlineMarkdown(value) {
  return value
    .replace(
      /\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
      '<a href="$2" target="_blank" rel="noreferrer">$1</a>',
    )
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/__([^_]+)__/g, "<strong>$1</strong>")
    .replace(/\*([^*]+)\*/g, "<em>$1</em>");
}
function markdown(value) {
  const blocks = [];
  let text = escape(value).replace(
    /```(?:[\w+-]+)?\n?([\s\S]*?)```/g,
    (_, code) => {
      const id = blocks.length;
      blocks.push(`<pre><code>${code.trimEnd()}</code></pre>`);
      return `\u0000CODE${id}\u0000`;
    },
  );
  text = text.replace(
    /(^\|.*\|\r?\n^\|(?:\s*:?-+:?\s*\|)+\r?\n(?:^\|.*\|(?:\r?\n|$))+)/gm,
    (table) => {
      const rows = table
        .trim()
        .split(/\r?\n/)
        .map((row) =>
          row
            .slice(1, -1)
            .split("|")
            .map((cell) => cell.trim()),
        );
      const headers = rows[0];
      const body = rows.slice(2);
      const html = `<div class="table-wrap"><table><thead><tr>${headers.map((cell) => `<th>${inlineMarkdown(cell)}</th>`).join("")}</tr></thead><tbody>${body.map((row) => `<tr>${row.map((cell) => `<td>${inlineMarkdown(cell)}</td>`).join("")}</tr>`).join("")}</tbody></table></div>`;
      const id = blocks.length;
      blocks.push(html);
      return `\u0000CODE${id}\u0000`;
    },
  );
  const output = [];
  let paragraph = [];
  let list = null;
  const flushParagraph = () => {
    if (paragraph.length) {
      output.push(`<p>${paragraph.map(inlineMarkdown).join("<br>")}</p>`);
      paragraph = [];
    }
  };
  const closeList = () => {
    if (list) {
      output.push(`</${list}>`);
      list = null;
    }
  };
  for (const line of text.split(/\r?\n/)) {
    const code = line.match(/^\u0000CODE(\d+)\u0000$/);
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    const unordered = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+\.\s+(.+)$/);
    const quote = line.match(/^\s*>\s?(.+)$/);
    if (!line.trim()) {
      flushParagraph();
      closeList();
      continue;
    }
    if (code) {
      flushParagraph();
      closeList();
      output.push(blocks[Number(code[1])]);
      continue;
    }
    if (heading) {
      flushParagraph();
      closeList();
      const level = Math.min(4, heading[1].length + 1);
      output.push(`<h${level}>${inlineMarkdown(heading[2])}</h${level}>`);
      continue;
    }
    if (unordered || ordered) {
      flushParagraph();
      const next = unordered ? "ul" : "ol";
      if (list !== next) {
        closeList();
        output.push(`<${next}>`);
        list = next;
      }
      output.push(`<li>${inlineMarkdown((unordered || ordered)[1])}</li>`);
      continue;
    }
    if (quote) {
      flushParagraph();
      closeList();
      output.push(`<blockquote>${inlineMarkdown(quote[1])}</blockquote>`);
      continue;
    }
    flushParagraph();
    paragraph.push(line);
  }
  flushParagraph();
  closeList();
  return output.join("");
}
const rawDisplayName = (session) =>
  session?.alias
    ? `@${session.alias}`
    : session?.name || session?.id || "Untitled conversation";
function displayName(session) {
  const raw = rawDisplayName(session);
  const repository = raw.match(/^https?:\/\/[^/]+\/([^/\s]+)\/([^/\s]+)/);
  return repository ? `${repository[1]} / ${repository[2]}` : raw;
}
const sessionBatchSize = () =>
  window.matchMedia("(max-width: 800px)").matches ? 8 : 80;
const timeAgo = (value) => {
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
function handoffMessage(value) {
  let text = String(value || "");
  let label = "Conversation input";
  let match;
  while ((match = text.match(/^\[from\s+([^\]]+)\]\s*/i))) {
    label = `From ${match[1]}`;
    text = text.slice(match[0].length);
  }
  return { label, text };
}
function renderMessage(text, kind, label) {
  const safeText = String(text || "");
  const isLong = safeText.length > 2400;
  return `<article class="turn ${kind}${isLong ? " long" : ""}"><header class="turn-header"><span class="turn-label">${escape(label)}</span></header><div class="turn-content${isLong ? " turn-crop" : ""}">${markdown(safeText)}</div>${isLong ? '<button class="expand-turn" type="button" data-expand-turn>Show full message</button>' : ""}</article>`;
}
function resizeComposer() {
  const input = $("#message");
  if (!input) return;
  const maxHeight = 160;
  input.style.height = "auto";
  const height = Math.min(input.scrollHeight, maxHeight);
  input.style.height = `${height}px`;
  input.style.overflowY = input.scrollHeight > maxHeight ? "auto" : "hidden";
}
function friendlyError(error) {
  return /failed to fetch|networkerror/i.test(String(error))
    ? "Agenthail cannot reach its local daemon. Start it with agenthail daemon start, then refresh this page."
    : String(error);
}
function toast(message) {
  const node = $("#toast");
  node.textContent = message;
  node.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => {
    node.hidden = true;
  }, 5000);
}
function showView(name) {
  const allowed = ["overview", "conversations", "operations"];
  const view = allowed.includes(name) ? name : "overview";
  document
    .querySelectorAll("[data-view-panel]")
    .forEach((panel) =>
      panel.classList.toggle("hidden", panel.dataset.viewPanel !== view),
    );
  document
    .querySelectorAll("[data-view]")
    .forEach((link) =>
      link.classList.toggle("active", link.dataset.view === view),
    );
  if (location.hash !== `#${view}`) history.replaceState(null, "", `#${view}`);
  if (view === "conversations" && app.hasState && !app.selected) {
    const first = scopedSessions()[0] || app.state.sessions[0];
    if (first) selectSession(first.id);
  }
}
function statusPill(status) {
  return `<span class="status-pill ${escape(status)}">${escape(statusLabel(status))}</span>`;
}
function statusLabel(status) {
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
function presenceLabel(session) {
  return (
    {
      working: "Working",
      queued: "Queued",
      open: "Open",
      recent: "Recent",
    }[session.currentReason] || statusLabel(session.status)
  );
}
function conversationMeta(session, model = "") {
  const parts = [statusLabel(session.status), `${session.queueCount || 0} queued`];
  if (session.status !== "busy") parts.push(`last active ${timeAgo(session.lastActive)}`);
  if (model) parts.push(model);
  return parts.join(" · ");
}
function surfaceIcon(name) {
  return name === "claude" ? "✦" : name === "codex" ? "◈" : "N";
}
function renderOverview() {
  const { surfaces, sessions } = app.state;
  $("#daemon-status").textContent = app.state.daemon?.running
    ? "Running locally"
    : "Not running";
  $("#daemon-detail").textContent = app.state.daemon?.stale
    ? `Showing cached data. ${app.state.daemon.refreshError || "Surface refresh is temporarily unavailable"}`
    : app.state.daemon?.running
      ? "Private and ready"
      : "Start the daemon to deliver work";
  $("#surface-cards").innerHTML =
    surfaces
      .map((surface) => {
        const owned = sessions.filter(
          (session) => session.surface === surface.name,
        );
        const active = owned.filter(
          (session) => session.currentReason === "working",
        ).length;
        const present = owned.filter((session) =>
          surface.name === "claude"
            ? session.open
            : session.currentReason === "recent",
        ).length;
        const queued = owned.reduce(
          (total, session) => total + session.queueCount,
          0,
        );
        const name = labels[surface.name] || surface.name;
        const presenceName = surface.name === "claude" ? "Open" : "Recent";
        return `<button class="surface-row" type="button" data-surface="${escape(surface.name)}"><div class="surface-identity"><span class="surface-logo ${escape(surface.name)}">${surfaceIcon(surface.name)}</span><div><div class="surface-name">${escape(name)}</div><div class="connection-line ${surface.connected ? "" : "offline"}"><i></i>${surface.connected ? "Connected" : surface.error ? escape(surface.error) : "Not connected"}</div></div></div><div class="surface-stat"><span>Working</span><strong>${active}</strong></div><div class="surface-stat"><span>${presenceName}</span><strong>${present}</strong></div><div class="surface-stat"><span>Queued</span><strong>${queued}</strong></div></button>`;
      })
      .join("") ||
    '<div class="empty-state compact">No surfaces are connected yet.</div>';
  const recent = [...sessions]
    .filter(sessionIsCurrent)
    .sort((a, b) => {
      const rank = { working: 0, queued: 1, open: 2, recent: 3 };
      const reasonOrder =
        (rank[a.currentReason] ?? 9) - (rank[b.currentReason] ?? 9);
      return (
        reasonOrder || new Date(b.lastActive || 0) - new Date(a.lastActive || 0)
      );
    })
    .slice(0, 6);
  $("#recent-activity").innerHTML =
    recent
      .map(
        (session) =>
          `<button class="activity-item" type="button" data-session="${escape(session.id)}"><div class="activity-main"><div class="activity-name"><i class="dot ${escape(session.status)}"></i><span>${escape(displayName(session))}</span></div><div class="activity-detail">${escape(labels[session.surface] || session.surface)} · ${session.status === "busy" ? "working now" : timeAgo(session.lastActive)}</div></div><span class="status-pill ${escape(session.currentReason || session.status)}">${escape(presenceLabel(session))}</span></button>`,
      )
      .join("") ||
    '<p class="empty-inline">No conversations are active right now.</p>';
}
function sessionIsCurrent(session) {
  return session.current === true;
}
function scopedSessions() {
  if (app.inboxMode === "current")
    return app.state.sessions.filter(sessionIsCurrent);
  return app.state.sessions;
}
function renderSessions() {
  const mobileViewport = window.matchMedia("(max-width: 800px)").matches;
  if (app.sessionViewport !== mobileViewport) {
    app.sessionViewport = mobileViewport;
    app.sessionLimit = sessionBatchSize();
  }
  if (!app.sessionLimit) app.sessionLimit = sessionBatchSize();
  const query = $("#session-search").value.trim().toLowerCase();
  const sessions = scopedSessions().filter((session) => {
    const matchesQuery =
      !query ||
      `${displayName(session)} ${session.surface} ${session.name}`
        .toLowerCase()
        .includes(query);
    const matchesSurface =
      app.filters.surface === "all" || session.surface === app.filters.surface;
    const matchesStatus =
      app.filters.status === "all" ||
      session.status === app.filters.status ||
      (app.filters.status === "unavailable" &&
        ["unknown", "notLoaded"].includes(session.status));
    return matchesQuery && matchesSurface && matchesStatus;
  });
  const visible = sessions.slice(0, app.sessionLimit);
  $("#conversation-count").textContent = `${sessions.length}`;
  $("#session-tools").classList.toggle("hidden", app.inboxMode !== "all");
  document
    .querySelectorAll("[data-inbox-mode]")
    .forEach((button) =>
      button.classList.toggle(
        "active",
        button.dataset.inboxMode === app.inboxMode,
      ),
    );
  document
    .querySelectorAll("[data-inbox-mode]")
    .forEach((button) =>
      button.setAttribute(
        "aria-pressed",
        String(button.dataset.inboxMode === app.inboxMode),
      ),
    );
  $("#filter-summary").textContent =
    app.inboxMode === "current"
      ? sessions.length
        ? `Open Claude sessions, working or queued work, and recent sessions (Codex ${app.state.codexRecentHours || 5}-hour window)`
        : "Nothing current right now"
      : sessions.length > visible.length
        ? `Showing ${visible.length} of ${sessions.length}`
        : `${sessions.length} conversations`;
  $("#session-list").innerHTML =
    visible
      .map(
        (session) =>
          `<button class="session ${app.selected?.id === session.id ? "selected" : ""}" title="${escape(rawDisplayName(session))}" type="button" data-session="${escape(session.id)}"><div class="session-name"><i class="dot ${escape(session.status)}"></i><span>${escape(displayName(session))}</span></div><div class="session-detail"><span>${escape(labels[session.surface] || session.surface)}</span><span>${escape(presenceLabel(session))}</span>${session.queueCount ? `<span>${session.queueCount} queued</span>` : ""}</div></button>`,
      )
      .join("") ||
    `<div class="empty-state compact"><p>${app.inboxMode === "current" ? "No current work. Open History to find an older conversation." : "No conversations in this view."}</p></div>`;
  const more = $("#session-more");
  more.hidden = app.inboxMode !== "all" || visible.length >= sessions.length;
  more.textContent = `Show more (${sessions.length - visible.length})`;
}
function renderSurfaceFilter() {
  const select = $("#session-surface-filter");
  const current = app.filters.surface;
  const options = [
    '<option value="all">All surfaces</option>',
    ...app.state.surfaces.map(
      (surface) =>
        `<option value="${escape(surface.name)}">${escape(labels[surface.name] || surface.name)}</option>`,
    ),
  ];
  select.innerHTML = options.join("");
  select.value = app.state.surfaces.some((surface) => surface.name === current)
    ? current
    : "all";
  app.filters.surface = select.value;
  $("#session-status-filter").value = app.filters.status;
}
function queueReason(item) {
  if (item.status !== "pending") return statusLabel(item.status);
  const target = app.state.sessions.find(
    (session) => session.id === item.sessionId,
  );
  return target?.status === "busy"
    ? "Waiting for this agent to finish"
    : "Waiting for delivery";
}
function renderOperations() {
  const { queue, channels, relays, history = [] } = app.state;
  $("#operations-queue-count").textContent = queue.length;
  $("#operations-relay-count").textContent = relays.length;
  $("#operations-history-count").textContent = history.length === 50 ? "50+" : history.length;
  $("#queue-list").innerHTML =
    queue
      .map(
        (item) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title"><i class="operation-dot ${escape(item.status)}"></i>${escape(item.target)}</div><div class="operation-detail">${escape(item.message)}${item.lastError ? `<br><span class="operation-error">${escape(item.lastError)}</span>` : ""}</div><div class="operation-meta">${escape(queueReason(item))} · queued ${timeAgo(item.queuedAt)}</div></div><div class="operation-actions">${statusPill(item.status)}${item.status === "dead" ? `<button class="button quiet" data-retry="${item.id}" type="button">Retry</button>` : ""}${item.status === "pending" ? `<button class="button quiet" data-cancel="${item.id}" type="button">Cancel</button>` : ""}</div></article>`,
      )
      .join("") ||
    '<div class="empty-card">Nothing is waiting to be delivered.</div>';
  $("#channel-list").innerHTML =
    channels
      .map(
        (channel) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title">#${escape(channel.name)}</div><div class="operation-detail">${escape(channel.members.join(" · ") || "No members yet")}</div></div><button class="button quiet" data-network-action="channel-delete" data-channel="${escape(channel.name)}" type="button">Remove</button></article>`,
      )
      .join("") || '<div class="empty-card">No shared channels yet.</div>';
  $("#relay-list").innerHTML =
    relays
      .map(
        (relay) =>
          `<article class="operation-item"><div class="operation-main"><div class="operation-title">${escape(relay.from)} <span class="route-arrow">→</span> ${escape(relay.to)}</div><div class="operation-detail">Matches /${escape(relay.pattern)}/</div></div><button class="button quiet" data-network-action="relay-remove" data-relay-id="${relay.id}" type="button">Remove</button></article>`,
      )
      .join("") || '<div class="empty-card">No automatic handoffs yet.</div>';
  renderAudit();
  document
    .querySelectorAll("[data-operations-panel]")
    .forEach((panel) =>
      panel.classList.toggle(
        "hidden",
        panel.dataset.operationsPanel !== app.operationsTab,
      ),
    );
  document
    .querySelectorAll("[data-operations-tab]")
    .forEach((tab) =>
      tab.classList.toggle(
        "active",
        tab.dataset.operationsTab === app.operationsTab,
      ),
    );
  document
    .querySelectorAll("[data-operations-tab]")
    .forEach((tab) =>
      tab.setAttribute(
        "aria-pressed",
        String(tab.dataset.operationsTab === app.operationsTab),
      ),
    );
}
function auditKindLabel(kind) {
  return String(kind || "event").replaceAll("_", " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}
function renderAudit() {
  const audit = app.audit;
  const kindSelect = $("#audit-kind-filter");
  kindSelect.innerHTML = ['<option value="all">All events</option>', ...audit.kinds.map((kind) => `<option value="${escape(kind)}">${escape(auditKindLabel(kind))}</option>`)].join("");
  kindSelect.value = audit.kinds.includes(audit.kind) ? audit.kind : "all";
  audit.kind = kindSelect.value;
  $("#history-list").innerHTML = audit.items.map((entry) => {
    const target = entry.target || entry.sessionId || "Agenthail";
    const route = entry.source ? `${escape(entry.source)} <span class="route-arrow">→</span> ${escape(target)}` : escape(target);
    const content = entry.error || entry.message || entry.result || "No message content recorded";
    return `<article class="audit-event"><div class="audit-marker ${entry.error ? "failed" : ""}"></div><div class="audit-event-body"><div class="audit-event-heading"><strong>${escape(auditKindLabel(entry.kind))}</strong><time>${timeAgo(entry.createdAt)}</time></div><div class="audit-route">${route}</div><details><summary>View details</summary><div class="audit-detail${entry.error ? " operation-error" : ""}">${markdown(content)}</div></details></div></article>`;
  }).join("") || `<div class="empty-card">${audit.loading ? "Loading activity." : audit.loaded ? "No activity matches these filters." : "Open Audit to load daemon activity."}</div>`;
  const more = $("#history-more");
  more.hidden = !audit.loaded || !audit.hasMore;
  more.disabled = audit.loading;
  more.textContent = audit.loading ? "Loading activity" : "Load older activity";
}
async function loadAudit(reset = false, silent = false) {
  if (app.audit.loading) return;
  const previous = silent ? { items: app.audit.items, nextBefore: app.audit.nextBefore, hasMore: app.audit.hasMore } : null;
  if (reset) app.audit = { ...app.audit, items: silent ? app.audit.items : [], nextBefore: 0, hasMore: true, loaded: silent && app.audit.loaded };
  if (!app.audit.hasMore) return;
  app.audit.loading = true;
  renderAudit();
  try {
    const params = new URLSearchParams({ limit: "25" });
    if (app.audit.nextBefore) params.set("before", app.audit.nextBefore);
    if (app.audit.kind !== "all") params.set("kind", app.audit.kind);
    if (app.audit.query) params.set("q", app.audit.query);
    const response = await fetch(`/api/history?${params}`);
    if (!response.ok) throw Error(await response.text());
    const page = await response.json();
    const previousIDs = previous ? new Set(previous.items.map((entry) => entry.id)) : null;
    const overlapsPrevious = previousIDs ? page.items.some((entry) => previousIDs.has(entry.id)) : false;
    if (reset) {
      if (previous) {
        const fresh = new Set(page.items.map((entry) => entry.id));
        app.audit.items = [...page.items, ...previous.items.filter((entry) => !fresh.has(entry.id))];
      } else {
        app.audit.items = page.items;
      }
    } else {
      const known = new Set(app.audit.items.map((entry) => entry.id));
      app.audit.items.push(...page.items.filter((entry) => !known.has(entry.id)));
    }
    app.audit.nextBefore = previous && overlapsPrevious ? previous.nextBefore : page.nextBefore || 0;
    app.audit.hasMore = previous && overlapsPrevious ? previous.hasMore : page.hasMore === true;
    app.audit.kinds = page.kinds || [];
    app.audit.loaded = true;
  } catch (error) {
    toast(friendlyError(error));
  } finally {
    app.audit.loading = false;
    renderAudit();
  }
}
function renderAll() {
  try {
    renderOverview();
  } catch (error) {
    console.error("dashboard overview render failed", error);
    $("#surface-cards").innerHTML =
      `<div class="empty-card">Could not render connected surfaces: ${escape(error.message || error)}</div>`;
  }
  renderSurfaceFilter();
  renderSessions();
  renderOperations();
  $("#sync").textContent =
    `Synced ${new Date(app.state.updatedAt || Date.now()).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`;
}
async function load(fresh = false) {
  if (app.loading) return;
  app.loading = true;
  const slowTimer = setTimeout(() => {
    if (app.loading && !app.hasState) {
      $("#sync").textContent = "Still connecting";
      $("#daemon-detail").textContent =
        "Surface discovery is taking a little longer";
      $("#surface-cards").innerHTML =
        '<div class="empty-state compact">Connecting to Claude, Codex, and Notion.</div>';
    }
  }, 2000);
  try {
    const response = await fetch(`/api/state${fresh ? "?fresh=1" : ""}`);
    if (!response.ok) throw Error(await response.text());
    app.state = await response.json();
    $("#daemon-presence").className = app.state.daemon?.running
      ? "daemon-presence online"
      : "daemon-presence offline";
    app.hasState = true;
    if (app.selected) {
      const replacement = app.state.sessions.find(
        (session) => session.id === app.selected.id,
      );
      if (replacement) app.selected = replacement;
    }
    renderAll();
    if (app.audit.loaded) await loadAudit(true, true);
    if (app.selected && app.history) renderChat();
    if (!app.selected && location.hash === "#conversations") {
      const first = scopedSessions()[0] || app.state.sessions[0];
      if (first) selectSession(first.id);
    }
  } catch (error) {
    const message = friendlyError(error);
    $("#daemon-status").textContent = "Daemon unavailable";
    $("#daemon-presence").className = "daemon-presence offline";
    $("#daemon-detail").textContent =
      "Run agenthail daemon start, then refresh";
    $("#surface-cards").innerHTML =
      `<div class="empty-state compact">${escape(message)}</div>`;
    $("#recent-activity").innerHTML = "";
    toast(message);
  } finally {
    clearTimeout(slowTimer);
    app.loading = false;
  }
}
async function selectSession(id, focus = false) {
  const session = app.state.sessions.find((item) => item.id === id);
  if (!session) return;
  app.selected = session;
  app.history = null;
  renderSessions();
  showView("conversations");
  $("#chat-surface").textContent = labels[session.surface] || session.surface;
  $("#chat-title").textContent = displayName(session);
  $("#chat-subtitle").textContent = conversationMeta(session);
  $("#message").disabled = false;
  $("#send").disabled = false;
  $("#message").placeholder = `Message ${displayName(session)}`;
  $("#composer-note").textContent =
    "Delivered through the local daemon. Busy agents are queued safely.";
  $("#chat-body").innerHTML =
    '<div class="empty-state">Loading recent messages…</div>';
  $("#chat-actions").innerHTML = "";
  try {
    const response = await fetch(
      `/api/session?id=${encodeURIComponent(id)}&limit=20`,
    );
    if (!response.ok) throw Error(await response.text());
    app.history = await response.json();
    renderChat();
    if (focus && window.matchMedia("(max-width: 800px)").matches)
      document
        .querySelector(".chat-pane")
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
  } catch (error) {
    $("#chat-body").innerHTML =
      `<div class="empty-state"><h2>Could not load this conversation</h2><p>${escape(error.message)}</p></div>`;
  }
}
function renderChat() {
  const { exchanges = [], goal, model, capabilities = {} } = app.history || {};
  const session = app.selected;
  const controls = [];
  if (session.status === "busy" && capabilities.steer)
    controls.push('<button data-action="steer" type="button">Steer</button>');
  if (session.status === "busy" && capabilities.interrupt)
    controls.push(
      '<button data-action="interrupt" type="button">Stop</button>',
    );
  if (capabilities.compact)
    controls.push(
      '<button data-action="compact" type="button">Compact</button>',
    );
  $("#chat-actions").innerHTML = controls.join("");
  $("#chat-subtitle").textContent = conversationMeta(session, model);
  $("#thread-count").textContent =
    `${exchanges.length} recent exchange${exchanges.length === 1 ? "" : "s"}`;
  const toolRows = [];
  if (capabilities.goal)
    toolRows.push(
      `<details class="session-details"><summary>Conversation settings</summary><form class="session-tools" data-tool="goal"><input name="goal" value="${escape(goal?.objective || "")}" placeholder="Set a focused goal"><button class="soft-button" type="submit">Save goal</button>${goal?.objective ? '<button class="soft-button" data-action="goal-clear" type="button">Clear</button>' : ""}</form></details>`,
    );
  const messages = exchanges
    .flatMap((exchange) => {
      const user = handoffMessage(exchange.user);
      const assistant = String(exchange.assistant || "");
      return [
        exchange.user ? renderMessage(user.text, "user", user.label) : "",
        assistant
          ? renderMessage(
              assistant,
              "agent",
              labels[session.surface] || session.surface,
            )
          : "",
      ];
    })
    .join("");
  $("#chat-body").innerHTML =
    `${toolRows.join("")}${messages || '<div class="empty-state"><span class="empty-glyph">✦</span><h2>No saved exchanges yet</h2><p>Send a message to start this conversation from Agenthail.</p></div>'}`;
  $("#chat-body").scrollTop = $("#chat-body").scrollHeight;
}
async function action(action, extra = {}) {
  const networkAction =
    action.startsWith("channel-") ||
    action.startsWith("relay-") ||
    action === "queue-retry" ||
    action === "queue-cancel";
  if (!app.selected && !networkAction)
    throw Error("Choose a conversation first");
  const response = await fetch("/api/action", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ action, sessionId: app.selected?.id, ...extra }),
  });
  if (!response.ok) throw Error(await response.text());
  return response.json();
}
async function send() {
  const message = $("#message").value.trim();
  if (!message) return;
  $("#send").disabled = true;
  try {
    const result = await action("send", { message });
    $("#message").value = "";
    resizeComposer();
    const queued = result.result?.disposition === "queued";
    toast(
      queued
        ? "This agent is busy, so your message is safely queued."
        : "Message sent.",
    );
    await load();
    await selectSession(app.selected.id);
  } catch (error) {
    toast(error.message);
  } finally {
    $("#send").disabled = false;
  }
}
document.addEventListener("click", async (event) => {
  const expand = event.target.closest("[data-expand-turn]");
  if (expand) {
    const turn = expand.closest(".turn");
    turn.classList.toggle("expanded");
    expand.textContent = turn.classList.contains("expanded")
      ? "Show less"
      : "Show full message";
    return;
  }
  const mode = event.target.closest("[data-inbox-mode]");
  if (mode) {
    app.inboxMode = mode.dataset.inboxMode;
    app.sessionLimit = sessionBatchSize();
    renderSessions();
    return;
  }
  const sessionButton = event.target.closest("[data-session]");
  if (sessionButton) return selectSession(sessionButton.dataset.session, true);
  const surfaceButton = event.target.closest("[data-surface]");
  if (surfaceButton) {
    showView("conversations");
    $("#session-search").value = "";
    app.inboxMode = "all";
    app.sessionLimit = sessionBatchSize();
    app.filters.surface = surfaceButton.dataset.surface;
    $("#session-surface-filter").value = app.filters.surface;
    renderSessions();
    return;
  }
  if (event.target.closest("[data-open-conversations]"))
    return showView("conversations");
  if (event.target.closest("[data-open-operations]"))
    return showView("operations");
  const retry = event.target.closest("[data-retry]");
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
  const control = event.target.closest("[data-action]");
  if (!control) return;
  control.disabled = true;
  try {
    await action(control.dataset.action, {
      message: $("#message").value.trim(),
    });
    toast(`${control.textContent} requested.`);
    await load();
    await selectSession(app.selected.id);
  } catch (error) {
    toast(error.message);
  } finally {
    control.disabled = false;
  }
});
$("#message").addEventListener("input", resizeComposer);
$("#composer").addEventListener("submit", (event) => {
  event.preventDefault();
  send();
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest('[data-tool="goal"]');
  if (!form) return;
  event.preventDefault();
  const input = form.querySelector("input");
  try {
    await action("goal-set", { message: input.value.trim() });
    toast("Goal saved.");
    await selectSession(app.selected.id);
  } catch (error) {
    toast(error.message);
  }
});
document.addEventListener("submit", async (event) => {
  const form = event.target.closest("[data-network-form]");
  if (!form) return;
  event.preventDefault();
  const actionName = form.dataset.networkForm;
  const values = Object.fromEntries(new FormData(form).entries());
  try {
    await action(actionName, values);
    toast(actionName === "relay-add" ? "Relay added." : "Network saved.");
    form.reset();
    await load();
  } catch (error) {
    toast(error.message);
  }
});
document.addEventListener("click", async (event) => {
  const button = event.target.closest("[data-network-action]");
  if (!button) return;
  button.disabled = true;
  try {
    await action(button.dataset.networkAction, {
      channel: button.dataset.channel,
      relayId: Number(button.dataset.relayId || 0),
    });
    toast("Network updated.");
    await load();
  } catch (error) {
    toast(friendlyError(error));
  }
});
$("#session-search").addEventListener("input", () => {
  app.sessionLimit = sessionBatchSize();
  renderSessions();
});
$("#session-surface-filter").addEventListener("change", (event) => {
  app.sessionLimit = sessionBatchSize();
  app.filters.surface = event.target.value;
  renderSessions();
});
$("#session-status-filter").addEventListener("change", (event) => {
  app.sessionLimit = sessionBatchSize();
  app.filters.status = event.target.value;
  renderSessions();
});
$("#session-more").addEventListener("click", () => {
  app.sessionLimit += sessionBatchSize();
  renderSessions();
});
document.addEventListener("click", (event) => {
  const tab = event.target.closest("[data-operations-tab]");
  if (tab) {
    app.operationsTab = tab.dataset.operationsTab;
    renderOperations();
    if (app.operationsTab === "audit" && !app.audit.loaded) loadAudit();
  }
});
$("#audit-search").addEventListener("input", (event) => {
  app.audit.query = event.target.value;
  clearTimeout(app.audit.searchTimer);
  app.audit.searchTimer = setTimeout(() => loadAudit(true), 250);
});
$("#audit-kind-filter").addEventListener("change", (event) => {
  app.audit.kind = event.target.value;
  loadAudit(true);
});
$("#history-more").addEventListener("click", () => loadAudit());
$("#refresh").addEventListener("click", async () => {
  await load(true);
});
window.addEventListener("resize", () => {
  renderSessions();
  resizeComposer();
});
window.addEventListener("hashchange", () => showView(location.hash.slice(1)));
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") load();
});
showView(location.hash.slice(1) || "overview");
load();
setInterval(() => {
  if (document.visibilityState === "visible") load();
}, 10000);
