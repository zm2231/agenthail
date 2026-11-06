import { $ } from "./dom.js";
import { renderAll } from "./dashboard-state.js";
import { catalogEvent, catalogStream } from "./catalog-live.js";
import { holdOrderWith, orderReleased, rereadSoon } from "./order-refresh.js";
import { bindOrderHold } from "./order-hold.js";
import { configureStream, startSharedStreams, streamsSupported, subscribe } from "./shared-stream.js";
import { toast } from "./feedback.js";
import { app } from "./state.js";

let painting = null;

function render() {
  if (painting) return;
  painting = setTimeout(() => {
    painting = null;
    renderAll();
  }, 250);
}

export function startLiveOrder() {
  holdOrderWith(bindOrderHold($("#session-list"), { onRelease: orderReleased }));
  if (!streamsSupported()) {
    toast("This browser can't show live updates. Use a current Chrome, Edge, Firefox, or Safari 15.4 or later.");
    return;
  }
  configureStream("catalog", catalogStream());
  subscribe("catalog", (message) => {
    if (message.kind === "event") catalogEvent(message.event, { render, reread: rereadSoon });
    else if (app.hasState) rereadSoon(0);
  });
  startSharedStreams();
}
