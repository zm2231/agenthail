import { backgroundURL, gradientLine, hex, imageRect, meshPixels, meshSize, regionColours, solveRegions } from "./ui-backdrop.js";

export function sampleSize({ width, height, scale }) {
  return { width: Math.max(Math.round(width * (scale || 1)), 1), height: Math.max(Math.round(height * (scale || 1)), 1) };
}

export function deviceRect([x, y, w, h], scale, width, height) {
  const left = Math.min(Math.max(Math.round(x * scale), 0), width);
  const top = Math.min(Math.max(Math.round(y * scale), 0), height);
  const right = Math.min(Math.max(Math.round((x + w) * scale), left), width);
  const bottom = Math.min(Math.max(Math.round((y + h) * scale), top), height);
  return [left, top, right - left, bottom - top];
}

export function createSampler({ makeCanvas, loadImage }) {
  const seen = new Uint8Array(1 << 21);
  let cached = null;

  const draw = async ({ spec, fit, position, width, height, scale, ground }) => {
    const size = sampleSize({ width, height, scale });
    const w = size.width;
    const h = size.height;
    const k = w / width;
    const context = makeCanvas(w, h).getContext("2d", { willReadFrequently: true });
    context.fillStyle = hex(ground);
    context.fillRect(0, 0, w, h);
    if (spec.kind === "image") {
      const image = await loadImage(backgroundURL(spec.path));
      if (!image) return null;
      const iw = image.width * k;
      const ih = image.height * k;
      if (fit === "tile") {
        for (let y = 0; y < h; y += ih) for (let x = 0; x < w; x += iw) context.drawImage(image, x, y, iw, ih);
      } else {
        const rect = imageRect(iw, ih, w, h, fit, position);
        context.drawImage(image, rect.x, rect.y, rect.width, rect.height);
      }
    } else if (spec.kind === "linear") {
      const line = gradientLine(spec.angle, w, h);
      const gradient = context.createLinearGradient(line.x0, line.y0, line.x1, line.y1);
      for (const stop of spec.stops) gradient.addColorStop(Math.min(Math.max(stop.location, 0), 1), hex(stop.colour));
      context.fillStyle = gradient;
      context.fillRect(0, 0, w, h);
    } else if (spec.kind === "mesh") {
      const mesh = meshSize(width / Math.max(height, 1));
      const small = makeCanvas(mesh.width, mesh.height);
      const smallContext = small.getContext("2d");
      const pixels = smallContext.createImageData(mesh.width, mesh.height);
      pixels.data.set(meshPixels(spec, mesh.width, mesh.height));
      smallContext.putImageData(pixels, 0, 0);
      context.drawImage(small, 0, 0, w, h);
    } else {
      return null;
    }
    return { data: context.getImageData(0, 0, w, h).data, width: w, height: h };
  };

  return {
    async measure(look) {
      const size = sampleSize(look);
      const key = JSON.stringify([look.spec, look.fit, look.position, size.width, size.height, look.ground]);
      if (cached?.key !== key) {
        const drawn = await draw(look);
        if (!drawn) return null;
        cached = { key, drawn, regions: new Map() };
      }
      const { drawn } = cached;
      const scale = drawn.width / look.width;
      const regions = look.regions.map((region) => {
        const rect = deviceRect(region.rect, scale, drawn.width, drawn.height);
        const rectKey = rect.join(",");
        if (!cached.regions.has(rectKey)) cached.regions.set(rectKey, regionColours(drawn.data, drawn.width, rect, seen));
        return { ...region, ...cached.regions.get(rectKey) };
      });
      return solveRegions(regions, look);
    },
  };
}
