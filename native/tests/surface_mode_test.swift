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
        check(!on.solidSurfaces(dark: true, reduceTransparency: false), "dark with glass on keeps glass")
        check(on.solidSurfaces(dark: false, reduceTransparency: false), "light is solid")
        check(on.solidSurfaces(dark: true, reduceTransparency: true), "Reduce Transparency is solid in dark")

        var off = UIConfig()
        off.values["glass"] = .text("off")
        check(off.solidSurfaces(dark: true, reduceTransparency: false), "glass off is solid in dark")
        check(off.solidSurfaces(dark: false, reduceTransparency: false), "glass off is solid in light")

        if failures > 0 { exit(1) }
        print("surface mode: light, glass off and Reduce Transparency draw solid surfaces")
    }
}
