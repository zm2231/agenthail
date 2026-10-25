import { sessionTarget } from "./actions.js";
import { friendlyError } from "./feedback.js";

export function deliveryStatusLabel(result) {
  const status = result?.result?.evidence || result?.status;
  return status === "queued" ? "Queued" : status === "submitted" ? "Submitted" : "Sent";
}

export function creationNotice(response, values) {
  const target = sessionTarget(response.session || { surface: values.surface, id: response.sessionId, alias: values.alias });
  return response.detail || response.warning || `${response.status === "submitted" ? "Submitted" : "Sent"} to ${target}.`;
}

export function creationFailureMessage(error) {
  return error?.unknown
    ? "Agenthail can't tell whether this session started; check the sidebar before trying again."
    : friendlyError(error);
}
