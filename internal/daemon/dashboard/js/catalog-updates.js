import { app } from "./state.js";
import { load } from "./dashboard-state.js";

let source;
let epoch;
let timer;

export function invalidatePresentation() {
  if (!timer) timer = setTimeout(() => { timer = null; load(); }, 150);
}

export function followCatalog(snapshot) {
  if (source && epoch === snapshot.hostEpoch) return;
  source?.close();
  epoch = snapshot.hostEpoch;
  app.seenCatalog = { epoch, sequence: snapshot.catalogSeq };
  const stream = new EventSource(`/api/v1/catalog-events?after=${snapshot.catalogSeq}`);
  source = stream;
  const changed = (event) => {
    if (source !== stream) return;
    const envelope = JSON.parse(event.data);
    app.seenCatalog.sequence = Math.max(app.seenCatalog.sequence, envelope.seq);
    invalidatePresentation();
  };
  for (const name of ["session.queue", "session.upserted", "session.removed", "session.rekeyed", "surface.health"]) stream.addEventListener(name, changed);
  stream.addEventListener("error", () => {
    if (source !== stream) return;
    stream.close();
    source = null;
    invalidatePresentation();
  });
}
