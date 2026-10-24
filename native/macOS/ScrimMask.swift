import CoreGraphics
import Foundation

enum ScrimMask {
    static let cell: CGFloat = 4

    struct Grid: Equatable {
        let columns: Int
        let rows: Int
        var values: [Float]

        func value(x: Int, y: Int) -> Float { values[y * columns + x] }
    }

    /// Grown by a cell so the grid scaled up linearly stays opaque inside every region.
    static func grid(size: CGSize, regions: [CGRect], feather: CGFloat) -> Grid {
        let columns = max(Int((size.width / cell).rounded(.up)), 1), rows = max(Int((size.height / cell).rounded(.up)), 1)
        var values = [Float](repeating: 0, count: columns * rows)
        let reach = feather * 2
        func falloff(_ d: CGFloat) -> Float {
            d <= 0 ? 1 : d >= reach ? 0 : Float(exp(-(d / feather) * (d / feather)))
        }
        for region in regions where region.width > 0 && region.height > 0 {
            let core = region.insetBy(dx: -cell, dy: -cell)
            let x0 = max(Int(((core.minX - reach) / cell).rounded(.down)), 0), x1 = min(Int(((core.maxX + reach) / cell).rounded(.up)), columns)
            let y0 = max(Int(((core.minY - reach) / cell).rounded(.down)), 0), y1 = min(Int(((core.maxY + reach) / cell).rounded(.up)), rows)
            guard x0 < x1, y0 < y1 else { continue }
            let gx = (x0..<x1).map { x -> Float in
                let lo = CGFloat(x) * cell, hi = lo + cell
                return falloff(max(core.minX - hi, lo - core.maxX, 0))
            }
            let gy = (y0..<y1).map { y -> Float in
                let lo = CGFloat(y) * cell, hi = lo + cell
                return falloff(max(core.minY - hi, lo - core.maxY, 0))
            }
            for (j, fy) in gy.enumerated() where fy > 0 {
                let row = (y0 + j) * columns
                for (i, fx) in gx.enumerated() {
                    let v = fx * fy
                    if v > values[row + x0 + i] { values[row + x0 + i] = v }
                }
            }
        }
        return Grid(columns: columns, rows: rows, values: values)
    }

    static func image(_ grid: Grid) -> CGImage? {
        guard let ctx = CGContext(data: nil, width: grid.columns, height: grid.rows, bitsPerComponent: 8, bytesPerRow: grid.columns * 4,
                                  space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = ctx.data else { return nil }
        let pixels = data.bindMemory(to: UInt8.self, capacity: grid.values.count * 4)
        for (i, v) in grid.values.enumerated() {
            let a = UInt8((min(max(v, 0), 1) * 255).rounded(.up))
            pixels[i * 4] = a; pixels[i * 4 + 1] = a; pixels[i * 4 + 2] = a; pixels[i * 4 + 3] = a
        }
        return ctx.makeImage()
    }
}
