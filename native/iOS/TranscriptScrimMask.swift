import CoreGraphics

struct TranscriptScrimMask: Equatable {
    static let feather: CGFloat = 6
    static let core: CGFloat = 2
    static var reach: CGFloat { core + feather * 2 }
    static let far: CGFloat = 100_000

    var layers: [CGRect]

    init(rows: [CGRect]) {
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
