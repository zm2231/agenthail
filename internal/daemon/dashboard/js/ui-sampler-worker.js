import { createSampler } from "./ui-sampler.js";

const sampler = createSampler({
  makeCanvas: (width, height) => new OffscreenCanvas(width, height),
  loadImage: async (url) => {
    try {
      const response = await fetch(url);
      return response.ok ? await createImageBitmap(await response.blob()) : null;
    } catch {
      return null;
    }
  },
});

self.onmessage = async ({ data: { id, look } }) => {
  let alpha = null;
  try {
    alpha = await sampler.measure(look);
  } catch {
    alpha = null;
  }
  self.postMessage({ id, alpha });
};
