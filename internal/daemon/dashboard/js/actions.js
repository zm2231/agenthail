import { app } from "./state.js";
import { withHolder } from "./holders.js";

export function isNetworkFailure(error) {
  return error?.networkFailure === true || error?.name === "TypeError";
}

export function logicalRequest(scope, actionName, payload) {
  const sessionId = scope === "session-create" ? "" : app.selected?.id || "";
  const serializedPayload = JSON.stringify({ action: actionName, sessionId, ...payload });
  const current = app.pendingIdempotency.get(scope);
  if (current?.fingerprint === serializedPayload) return current;
  const next = { fingerprint: serializedPayload, key: crypto.randomUUID(), sessionId };
  app.pendingIdempotency.set(scope, next);
  return next;
}

export function clearLogicalRequest(scope) {
  app.pendingIdempotency.delete(scope);
}

export function sessionTarget(session) {
  if (session?.alias) return `@${session.alias}`;
  return `${session?.surface || "session"}:${session?.id || "unknown"}`;
}

export async function logicalAction(scope, actionName, payload) {
  payload = withHolder(app.selected?.id, payload);
  const request = logicalRequest(scope, actionName, payload);
  try {
    const result = await action(actionName, payload, request.key, request.sessionId);
    clearLogicalRequest(scope);
    return result;
  } catch (error) {
    if (!isNetworkFailure(error) && !error.unknown) clearLogicalRequest(scope);
    throw error;
  }
}

export async function action(action, extra = {}, idempotencyKey = crypto.randomUUID(), sessionID = app.selected?.id) {
  const networkAction =
    action.startsWith("channel-") ||
    action.startsWith("relay-") ||
    action === "notion-create" ||
    action === "session-create" ||
    action === "queue-retry" ||
    action === "delivery-dismiss" ||
    action === "queue-cancel" ||
    action.startsWith("project-") ||
    action.startsWith("coordinator-");
  if (!app.selected && !networkAction)
    throw Error("Choose a conversation first");
  if (!networkAction) extra = withHolder(sessionID, extra);
  let response;
  try {
    response = await fetch("/api/action", {
      method: "POST",
      headers: { "content-type": "application/json", "Idempotency-Key": idempotencyKey },
      body: JSON.stringify({ action, sessionId: sessionID, ...extra }),
    });
  } catch (error) {
    error.networkFailure = true;
    throw error;
  }
  if (!response.ok) {
    let detail;
    try {
      detail = await response.text();
    } catch (error) {
      const typed = Error(`HTTP ${response.status || 500}`);
      typed.httpFailure = true;
      throw typed;
    }
    const failure = Error(detail);
    try {
      const body = JSON.parse(detail);
      const typed = body.error;
      if (typed?.message) {
        failure.message = typed.message;
        failure.code = typed.code || "";
      } else if (typeof typed === "string") {
        failure.message = typed;
        failure.code = body.code || "";
      }
      failure.unknown = body.unknown === true || ["host_outcome_unknown", "session_operation_unknown", "session_adopt_unknown", "move_outcome_unknown", "create_unknown"].includes(failure.code);
    } catch {}
    throw failure;
  }
  try {
    return await response.json();
  } catch (error) {
    error.networkFailure = true;
    throw error;
  }
}
