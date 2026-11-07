import CoreGraphics

struct TranscriptScrimMask: Equatable {
    enum Shape: Equatable {
        case band
        case panels
    }

    static let feather: CGFloat = 6
    static let core: CGFloat = 2
    static var reach: CGFloat { core + feather * 2 }
    static let far: CGFloat = 100_000
    // Wider than any laid-out gap between rows: a wider space holds rows not reported yet.
    static let unreported: CGFloat = 32

    struct Row: Equatable {
        let rect: CGRect
        var turn: String? = nil
    }

    var layers: [CGRect]

    init(rows: [CGRect], shape: Shape = .band, end: CGFloat? = nil) {
        self.init(turns: rows.map { Row(rect: $0) }, shape: shape, end: end)
    }

    init(turns rows: [Row], shape: Shape = .band, end: CGFloat? = nil) {
        switch shape {
        case .band: self.init(band: rows.map(\.rect))
        case .panels: self.init(panels: rows, end: end)
        }
    }

    static func layer(row: CGRect) -> CGRect? {
        row.width > 0 && row.height > 0 ? row.insetBy(dx: -reach, dy: -reach) : nil
    }

    private init(panels rows: [Row], end: CGFloat?) {
        let rows = rows.filter { $0.rect.width > 0 && $0.rect.height > 0 }.sorted { $0.rect.minY < $1.rect.minY }
        guard let first = rows.first, let left = rows.map(\.rect.minX).min(), let right = rows.map(\.rect.maxX).max() else {
            layers = []
            return
        }
        func span(_ top: CGFloat, _ bottom: CGFloat) -> CGRect { CGRect(x: left, y: top, width: right - left, height: bottom - top) }
        var cores = [span(-Self.far, first.rect.minY)]
        var panel = span(first.rect.minY, first.rect.maxY)
        var turn = first.turn
        for row in rows.dropFirst() {
            let gap = row.rect.minY - panel.maxY
            let opens = turn != nil && row.turn != nil && row.turn != turn
            if opens, gap > 0, gap <= Self.unreported {
                cores.append(panel)
                panel = span(row.rect.minY, row.rect.maxY)
            } else {
                panel = panel.union(span(row.rect.minY, row.rect.maxY))
            }
            if let next = row.turn { turn = next }
        }
        if let end, end > panel.maxY { panel = panel.union(span(panel.maxY, end)) }
        cores.append(panel)
        layers = cores.map { $0.insetBy(dx: -Self.reach, dy: -Self.reach) }
    }

    private init(band rows: [CGRect]) {
        var cores = Self.merged(rows.filter { $0.width > 0 && $0.height > 0 })
        if let top = cores.map(\.minY).min(), let bottom = cores.map(\.maxY).max(),
           let left = cores.map(\.minX).min(), let right = cores.map(\.maxX).max() {
            // Rows laid out mid-scroll land under scrim before they report.
            cores.append(CGRect(x: left, y: -Self.far, width: right - left, height: Self.far + top))
            cores.append(CGRect(x: left, y: bottom, width: right - left, height: Self.far))
        } else {
            cores = [CGRect(x: -Self.far, y: -Self.far, width: Self.far * 2, height: Self.far * 2)]
        }
        layers = cores.map { $0.insetBy(dx: -Self.reach, dy: -Self.reach) }
    }

    static func merged(_ rows: [CGRect]) -> [CGRect] {
        var out: [CGRect] = []
        for row in rows.sorted(by: { $0.minY < $1.minY }) {
            if let last = out.last, row.minY - last.maxY < reach * 2 {
                out[out.count - 1] = last.union(row)
            } else {
                out.append(row)
            }
        }
        return out
    }

    static func falloff(_ d: CGFloat) -> CGFloat {
        d <= 0 ? 1 : d >= feather * 2 ? 0 : exp(-(d / feather) * (d / feather))
    }

    static func image(scale: CGFloat) -> CGImage? {
        let side = Int(((reach * 2 + 1) * scale).rounded(.up))
        guard let ctx = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: side * 4,
                                  space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = ctx.data else { return nil }
        let pixels = data.bindMemory(to: UInt8.self, capacity: side * side * 4)
        let edge = reach * 2 + 1
        let values = (0..<side).map { i -> CGFloat in
            let p = (CGFloat(i) + 0.5) / scale
            return falloff(reach - core - min(p, edge - p))
        }
        for y in 0..<side {
            for x in 0..<side {
                let a = UInt8((values[x] * values[y] * 255).rounded(.up))
                let i = (y * side + x) * 4
                pixels[i] = a; pixels[i + 1] = a; pixels[i + 2] = a; pixels[i + 3] = a
            }
        }
        return ctx.makeImage()
    }

    static var stretch: CGRect {
        let edge = reach * 2 + 1
        return CGRect(x: reach / edge, y: reach / edge, width: 1 / edge, height: 1 / edge)
    }
}
