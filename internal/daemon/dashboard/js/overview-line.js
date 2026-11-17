import { app } from "./state.js";
import { $ } from "./dom.js";
import { currentStops } from "./decision-card.js";
import { overviewLine, overviewWorking } from "./overview-counts.js";

export function renderOverviewLine() {
  const { surfaces = [], sessions = [] } = app.state || {};
  const detail = $("#daemon-detail");
  if (!detail || !app.state) return;
  const working = overviewWorking(app.sessionOrder?.current?.view?.summary, sessions);
  detail.textContent = overviewLine({ daemon: app.state.daemon, surfaces, sessions, working, needsYou: currentStops().count });
}
