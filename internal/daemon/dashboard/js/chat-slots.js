export const chatSlots = 3;

function hold(locks, name, options, onLost) {
  return new Promise((resolve) => {
    let release;
    const released = new Promise((done) => { release = done; });
    locks.request(name, options, (lock) => {
      if (!lock) {
        resolve(null);
        return undefined;
      }
      resolve({ release });
      return released;
    }).catch(() => {
      resolve(null);
      onLost();
    });
  });
}

export async function claimChatSlot(onLost, { locks = globalThis.navigator?.locks, focused = () => document.hasFocus(), random = Math.random } = {}) {
  if (!locks) return null;
  for (let slot = 0; slot < chatSlots; slot++) {
    const held = await hold(locks, `agenthail-chat-${slot}`, { ifAvailable: true }, onLost);
    if (held) return held;
  }
  if (!focused()) return null;
  return hold(locks, `agenthail-chat-${Math.floor(random() * chatSlots)}`, { steal: true }, onLost);
}
