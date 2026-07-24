import SwiftUI

@main
struct ResponsivePairLayoutTest {
    static func main() {
        let widths = [717.0, 718.0, 743.0, 744.0]
        for width in widths {
            let horizontal = width >= 718
            let columns = ResponsivePairLayout.horizontalWidths(
                available: width,
                spacing: 38,
                minimumLeadingWidth: 320,
                minimumTrailingWidth: 360
            )
            if horizontal {
                precondition(columns.leading >= 320)
                precondition(columns.trailing >= 360)
                precondition(abs(columns.leading + columns.trailing + 38 - width) < 0.001)
            }
        }
        print("responsive pair layout tests passed")
    }
}
