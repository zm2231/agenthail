@testable import AgenthailHelpers
import Foundation

@main
struct TranscriptColumnTest {
    static func main() {
        for pane in stride(from: CGFloat(200), through: 2600, by: 37) {
            let layout = DesktopColumns.transcript(pane: pane)
            check(layout.column == min(pane - 2 * DesktopColumns.gutter, DesktopColumns.reading), "column is the pane less its gutters, up to the reading width, at \(pane)")
            check(layout.overhang >= 0 && layout.overhang <= DesktopColumns.wide - DesktopColumns.reading, "a tool run reaches at most to the wide width, at \(pane)")
            check(DesktopColumns.gutter + layout.column + layout.overhang <= pane - DesktopColumns.gutter + 0.5, "a tool run stops at the right gutter, at \(pane)")
        }
        check(DesktopColumns.transcript(pane: 600).overhang == 0, "a pane that fits only the column gives tool runs nothing extra")
        check(DesktopColumns.transcript(pane: 1440) == (880, 240), "a wide pane gives the full reading column and the full overhang")
        check(DesktopColumns.transcript(pane: 40).column == 0, "a pane narrower than its gutters has no column, not a negative one")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
