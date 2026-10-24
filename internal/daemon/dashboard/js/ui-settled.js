export function settledTracker(element) {
  let pending = 0;
  return (promise) => {
    pending++;
    element.toggleAttribute?.("data-ui-settled", false);
    Promise.resolve(promise).finally(() => {
      if (--pending === 0) element.toggleAttribute?.("data-ui-settled", true);
    });
    return promise;
  };
}

export function debounced(run, delay, track) {
  let timer = null;
  let release = null;
  return () => {
    clearTimeout(timer);
    const previous = release;
    track(
      new Promise((resolve) => {
        release = resolve;
        timer = setTimeout(() => {
          release = null;
          try {
            run();
          } finally {
            resolve();
          }
        }, delay);
      }),
    );
    previous?.();
  };
}
