import Foundation

/// The computed layer that keeps text readable over a background. Every
/// buffer is row-major at one value per point; colours are sRGB in 0...1.
enum ScrimField {
    struct Raster {
        let width: Int
        let height: Int
        var rgb: [Float]

        init(width: Int, height: Int, rgb: [Float]) {
            precondition(rgb.count == width * height * 3)
            self.width = width
            self.height = height
            self.rgb = rgb
        }

        init(width: Int, height: Int, fill: (Float, Float, Float)) {
            self.init(width: width, height: height, rgb: Array([fill.0, fill.1, fill.2].repeated(width * height)))
        }
    }

    struct Rect: Equatable {
        var x: Int
        var y: Int
        var width: Int
        var height: Int

        func clamped(width w: Int, height h: Int) -> Rect {
            let x0 = min(max(x, 0), w), y0 = min(max(y, 0), h)
            let x1 = min(max(x + width, 0), w), y1 = min(max(y + height, 0), h)
            return Rect(x: x0, y: y0, width: max(x1 - x0, 0), height: max(y1 - y0, 0))
        }
    }

    enum Polarity { case dark, light }

    enum Tint: Equatable {
        case auto
        case color(Float, Float, Float)
    }

    static let pad = 8
    static let paneSmoothing = 16
    static let margin: Float = 0.1
    static let tintDepth: Float = 0.6

    static func linear(_ c: Float) -> Float {
        c <= 0.04045 ? c / 12.92 : powf((c + 0.055) / 1.055, 2.4)
    }

    static func gamma(_ l: Float) -> Float {
        l <= 0.0031308 ? l * 12.92 : 1.055 * powf(max(l, 0), 1 / 2.4) - 0.055
    }

    static func luminance(_ r: Float, _ g: Float, _ b: Float) -> Float {
        0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b)
    }

    /// The background luminance every role still reads on: an upper bound
    /// for light text on dark, a lower bound for dark text on light.
    static func boundary(roles: [Float], polarity: Polarity, target: Float) -> Float {
        let goal = target + margin
        switch polarity {
        case .dark: return roles.map { ($0 + 0.05) / goal - 0.05 }.min() ?? 0
        case .light: return roles.map { ($0 + 0.05) * goal - 0.05 }.max() ?? 1
        }
    }

    static func passes(_ shown: Float, boundary: Float, polarity: Polarity) -> Bool {
        polarity == .dark ? shown <= boundary : shown >= boundary
    }

    /// The tint behind text: a fixed colour, or with auto the background's
    /// own colour, heavily blurred and moved past the boundary so the floor
    /// is always reachable.
    static func tint(for image: Raster, mode: Tint, polarity: Polarity, boundary: Float) -> Raster {
        if case .color(let r, let g, let b) = mode {
            return Raster(width: image.width, height: image.height, fill: (r, g, b))
        }
        let cell = 4
        let gw = max(image.width / cell, 1), gh = max(image.height / cell, 1)
        var grid = [Float](repeating: 0, count: gw * gh * 3)
        for gy in 0..<gh {
            for gx in 0..<gw {
                var sum: (Float, Float, Float) = (0, 0, 0)
                var count: Float = 0
                for y in (gy * cell)..<min((gy + 1) * cell, image.height) {
                    for x in (gx * cell)..<min((gx + 1) * cell, image.width) {
                        let i = (y * image.width + x) * 3
                        sum.0 += linear(image.rgb[i]); sum.1 += linear(image.rgb[i + 1]); sum.2 += linear(image.rgb[i + 2])
                        count += 1
                    }
                }
                let i = (gy * gw + gx) * 3
                grid[i] = sum.0 / count; grid[i + 1] = sum.1 / count; grid[i + 2] = sum.2 / count
            }
        }
        for channel in 0..<3 {
            var plane = (0..<(gw * gh)).map { grid[$0 * 3 + channel] }
            plane = gaussian(plane, width: gw, height: gh, sigma: 20, radius: 60)
            for i in 0..<(gw * gh) { grid[i * 3 + channel] = plane[i] }
        }
        let target = polarity == .dark ? boundary * tintDepth : 1 - (1 - boundary) * tintDepth
        for i in 0..<(gw * gh) {
            let l = (grid[i * 3], grid[i * 3 + 1], grid[i * 3 + 2])
            let y = 0.2126 * l.0 + 0.7152 * l.1 + 0.0722 * l.2
            var out: (Float, Float, Float)
            if polarity == .dark {
                let scale = target / max(y, 1e-6)
                out = (min(l.0 * scale, 1), min(l.1 * scale, 1), min(l.2 * scale, 1))
            } else {
                let scale = (1 - target) / max(1 - y, 1e-6)
                out = (1 - (1 - l.0) * scale, 1 - (1 - l.1) * scale, 1 - (1 - l.2) * scale)
            }
            grid[i * 3] = gamma(out.0); grid[i * 3 + 1] = gamma(out.1); grid[i * 3 + 2] = gamma(out.2)
        }
        return upsample(grid, width: gw, height: gh, channels: 3, to: image.width, image.height, as: Raster.self)
    }

    /// The lowest tint opacity at each point of rect at which the shown
    /// colour passes the boundary. Blending is in linear light, as Core
    /// Image composites, so the shown luminance is linear in the opacity.
    static func floor(image: Raster, tint: Raster, in rect: Rect, polarity: Polarity, boundary: Float) -> [Float] {
        var out = [Float](repeating: 0, count: rect.width * rect.height)
        for y in 0..<rect.height {
            for x in 0..<rect.width {
                let i = ((rect.y + y) * image.width + rect.x + x) * 3
                let lb = luminance(image.rgb[i], image.rgb[i + 1], image.rgb[i + 2])
                guard !passes(lb, boundary: boundary, polarity: polarity) else { continue }
                let lt = luminance(tint.rgb[i], tint.rgb[i + 1], tint.rgb[i + 2])
                let a = lb == lt ? 1 : (lb - boundary) / (lb - lt)
                out[y * rect.width + x] = (min(max(a, 0), 1) * 255).rounded(.up) / 255
            }
        }
        return out
    }

    static func needs(image: Raster, tint: Raster, texts: [Rect], polarity: Polarity, boundary: Float) -> [Float] {
        var need = [Float](repeating: 0, count: image.width * image.height)
        for text in texts {
            let rect = text.clamped(width: image.width, height: image.height)
            let local = floor(image: image, tint: tint, in: rect, polarity: polarity, boundary: boundary)
            for y in 0..<rect.height {
                for x in 0..<rect.width {
                    let i = (rect.y + y) * image.width + rect.x + x
                    need[i] = max(need[i], local[y * rect.width + x])
                }
            }
        }
        return need
    }

    /// Opacity for a pane: the floor grown by r, then smoothed with a kernel
    /// no wider than r, so no point drops below its own floor.
    static func paneOpacity(need: [Float], width: Int, height: Int, minimum: Float) -> [Float] {
        let r = paneSmoothing
        let grown = maxFilter(need, width: width, height: height, radius: r)
        return gaussian(grown, width: width, height: height, sigma: Float(r) / 3, radius: r).map { max($0, minimum) }
    }

    /// Opacity for open areas: the floor holds over the padded text, and
    /// outside it the scrim fades with distance as exp(-(d/feather)^2).
    static func openOpacity(need: [Float], width: Int, height: Int, texts: [Rect], feather: Float, minimum: Float) -> [Float] {
        let cell = 4
        let gw = max((width + cell - 1) / cell, 1), gh = max((height + cell - 1) / cell, 1)
        var small = [Float](repeating: 0, count: gw * gh)
        for y in 0..<height {
            for x in 0..<width {
                let i = (y / cell) * gw + x / cell
                small[i] = max(small[i], need[y * width + x])
            }
        }
        var inside = [Bool](repeating: false, count: gw * gh)
        for text in texts {
            let x0 = max((text.x - pad) / cell, 0), y0 = max((text.y - pad) / cell, 0)
            let x1 = min((text.x + text.width + pad + cell - 1) / cell, gw), y1 = min((text.y + text.height + pad + cell - 1) / cell, gh)
            guard x0 < x1, y0 < y1 else { continue }
            for y in y0..<y1 { for x in x0..<x1 { inside[y * gw + x] = true } }
        }
        let core = maxFilter(small, width: gw, height: gh, radius: 2)
        var edges: [(Int, Int, Float)] = []
        for y in 0..<gh {
            for x in 0..<gw where inside[y * gw + x] {
                let border = [(x - 1, y), (x + 1, y), (x, y - 1), (x, y + 1)].contains { nx, ny in
                    nx < 0 || ny < 0 || nx >= gw || ny >= gh || !inside[ny * gw + nx]
                }
                if border { edges.append((x, y, core[y * gw + x])) }
            }
        }
        let f = max(feather / Float(cell), 1)
        var field = [Float](repeating: 0, count: gw * gh)
        for y in 0..<gh {
            for x in 0..<gw {
                let i = y * gw + x
                if inside[i] { field[i] = core[i]; continue }
                var best: Float = .infinity, value: Float = 0
                for (ex, ey, v) in edges {
                    let d = Float((ex - x) * (ex - x) + (ey - y) * (ey - y))
                    if d < best { best = d; value = v }
                }
                if best.isFinite { field[i] = value * expf(-best / (f * f)) }
            }
        }
        let full: [Float] = upsample(field, width: gw, height: gh, channels: 1, to: width, height, as: [Float].self)
        return full.map { max($0, minimum) }
    }

    static func composite(_ base: Raster, tint: Raster, alpha: [Float]) -> Raster {
        var out = base
        for i in 0..<(base.width * base.height) {
            let a = alpha[i]
            for c in 0..<3 {
                out.rgb[i * 3 + c] = gamma(linear(tint.rgb[i * 3 + c]) * a + linear(base.rgb[i * 3 + c]) * (1 - a))
            }
        }
        return out
    }

    static func maxFilter(_ values: [Float], width: Int, height: Int, radius: Int) -> [Float] {
        var rows = values
        for y in 0..<height {
            for x in 0..<width {
                var m: Float = 0
                for k in max(x - radius, 0)...min(x + radius, width - 1) { m = max(m, values[y * width + k]) }
                rows[y * width + x] = m
            }
        }
        var out = rows
        for y in 0..<height {
            for x in 0..<width {
                var m: Float = 0
                for k in max(y - radius, 0)...min(y + radius, height - 1) { m = max(m, rows[k * width + x]) }
                out[y * width + x] = m
            }
        }
        return out
    }

    static func gaussian(_ values: [Float], width: Int, height: Int, sigma: Float, radius: Int) -> [Float] {
        let kernel = (-radius...radius).map { expf(-Float($0 * $0) / (2 * sigma * sigma)) }
        func pass(_ input: [Float], horizontal: Bool) -> [Float] {
            var output = input
            for y in 0..<height {
                for x in 0..<width {
                    var sum: Float = 0, weight: Float = 0
                    for (j, k) in kernel.enumerated() {
                        let o = j - radius
                        let (sx, sy) = horizontal ? (x + o, y) : (x, y + o)
                        guard sx >= 0, sy >= 0, sx < width, sy < height else { continue }
                        sum += input[sy * width + sx] * k
                        weight += k
                    }
                    output[y * width + x] = sum / weight
                }
            }
            return output
        }
        return pass(pass(values, horizontal: true), horizontal: false)
    }

    static func upsample<T>(_ grid: [Float], width gw: Int, height gh: Int, channels: Int, to width: Int, _ height: Int, as: T.Type) -> T {
        var out = [Float](repeating: 0, count: width * height * channels)
        let sx = Float(gw) / Float(width), sy = Float(gh) / Float(height)
        for y in 0..<height {
            let fy = max((Float(y) + 0.5) * sy - 0.5, 0)
            let y0 = min(Int(fy), gh - 1), y1 = min(y0 + 1, gh - 1), ty = fy - Float(y0)
            for x in 0..<width {
                let fx = max((Float(x) + 0.5) * sx - 0.5, 0)
                let x0 = min(Int(fx), gw - 1), x1 = min(x0 + 1, gw - 1), tx = fx - Float(x0)
                for c in 0..<channels {
                    let a = grid[(y0 * gw + x0) * channels + c], b = grid[(y0 * gw + x1) * channels + c]
                    let d = grid[(y1 * gw + x0) * channels + c], e = grid[(y1 * gw + x1) * channels + c]
                    out[(y * width + x) * channels + c] = (a * (1 - tx) + b * tx) * (1 - ty) + (d * (1 - tx) + e * tx) * ty
                }
            }
        }
        if T.self == Raster.self { return Raster(width: width, height: height, rgb: out) as! T }
        return out as! T
    }
}

private extension Array {
    func repeated(_ count: Int) -> [Element] {
        var out: [Element] = []
        out.reserveCapacity(self.count * count)
        for _ in 0..<count { out.append(contentsOf: self) }
        return out
    }
}
