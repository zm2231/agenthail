@testable import AgenthailHelpers
import CoreGraphics

@main
struct ScrimMaskTest {
    static func sample(_ grid: ScrimMask.Grid, _ x: CGFloat, _ y: CGFloat) -> Float {
        let fx = max(x / ScrimMask.cell - 0.5, 0), fy = max(y / ScrimMask.cell - 0.5, 0)
        let x0 = min(Int(fx), grid.columns - 1), y0 = min(Int(fy), grid.rows - 1)
        let x1 = min(x0 + 1, grid.columns - 1), y1 = min(y0 + 1, grid.rows - 1)
        let tx = Float(fx) - Float(x0), ty = Float(fy) - Float(y0)
        let top = grid.value(x: x0, y: y0) * (1 - tx) + grid.value(x: x1, y: y0) * tx
        let bottom = grid.value(x: x0, y: y1) * (1 - tx) + grid.value(x: x1, y: y1) * tx
        return top * (1 - ty) + bottom * ty
    }

    static func main() {
        var failures = 0
        let size = CGSize(width: 400, height: 300)
        let a = CGRect(x: 37.5, y: 41, width: 101, height: 33)
        let b = CGRect(x: 170, y: 50, width: 90, height: 40)
        let both = ScrimMask.grid(size: size, regions: [a, b], feather: 40)
        for rect in [a, b] {
            var y = rect.minY
            while y <= rect.maxY {
                var x = rect.minX
                while x <= rect.maxX {
                    if sample(both, x, y) < 0.999 {
                        print("FAIL: inside \(rect) at (\(x), \(y)) the mask is \(sample(both, x, y))")
                        failures += 1
                    }
                    x += 1
                }
                y += 1
            }
        }
        let alone = [ScrimMask.grid(size: size, regions: [a], feather: 40), ScrimMask.grid(size: size, regions: [b], feather: 40)]
        for i in both.values.indices where abs(both.values[i] - max(alone[0].values[i], alone[1].values[i])) > 1e-6 {
            print("FAIL: overlapping feathers are not max-blended at cell \(i)")
            failures += 1
            break
        }
        if sample(both, 380, 280) != 0 {
            print("FAIL: the mask reaches past twice the feather")
            failures += 1
        }
        if failures > 0 { exit(1) }
        print("scrim mask: opaque inside every region after scaling, max-blended where feathers overlap, clear far away")
    }
}
