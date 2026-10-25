const epochs = new Map();

export function retiredPresentation(position) {
  return epochs.get(position.hostId)?.retired.has(position.epoch) || false;
}

export function rememberPresentation(position) {
  let state = epochs.get(position.hostId);
  if (!state) {
    state = { epoch: position.epoch, retired: new Set() };
    epochs.set(position.hostId, state);
  } else if (state.epoch !== position.epoch && !state.retired.has(position.epoch)) {
    state.retired.add(state.epoch);
    state.epoch = position.epoch;
  }
}
