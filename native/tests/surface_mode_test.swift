@testable import AgenthailHelpers
import Foundation

@main
struct SurfaceModeTest {
    static func main() {
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }

        let on = UIConfig()
        check(!on.solidSurfaces(reduceTransparency: false), "glass on keeps glass in light and dark")
        check(on.solidSurfaces(reduceTransparency: true), "Reduce Transparency is solid")

        var off = UIConfig()
        off.values["glass"] = .text("off")
        check(off.solidSurfaces(reduceTransparency: false), "glass off is solid")

        if failures > 0 { exit(1) }
        print("surface mode: glass off and Reduce Transparency draw solid surfaces")
    }
}
