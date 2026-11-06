import { app } from "./state.js";
import { load } from "./dashboard-state.js";

let timer = null;
let running = false;
let again = false;
let deferred = false;
let held = () => false;

export function holdOrderWith(holding) {
  held = holding;
}

async function run() {
  timer = null;
  if (held()) {
    deferred = true;
    return;
  }
  if (document.hidden) return;
  if (app.loading) {
    timer = setTimeout(run, 300);
    return;
  }
  running = true;
  try {
    await load(false, false);
  } finally {
    running = false;
  }
  if (again) {
    again = false;
    rereadSoon();
  }
}

export function rereadSoon(delay = 300) {
  if (running) {
    again = true;
    return;
  }
  if (timer === null) timer = setTimeout(run, delay);
}

export function orderReleased() {
  if (!deferred) return;
  deferred = false;
  rereadSoon(0);
}
