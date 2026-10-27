import { escape } from "./format.js";

export function limitMoveButton(offer) {
  const name = offer.account || offer.root;
  return `<button data-limit-move="${escape(offer.root)}" data-limit-move-name="${escape(name)}" data-limit-move-reason="${escape(offer.reason || "")}" type="button">Move to ${escape(name)}</button>`;
}

export function limitMoveQuestion(control) {
  const { limitMoveName: name, limitMoveReason: reason } = control.dataset;
  return `Move this session to ${name}${reason ? ` (${reason})` : ""}? It stops here and continues under that account.`;
}
