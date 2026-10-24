import SwiftUI

struct WorkingSpinner: View {
    var body: some View {
        Image(systemName: "progress.indicator")
            .font(.system(size: 10, weight: .semibold))
            .foregroundStyle(DesktopPalette.working)
            .symbolEffect(.variableColor.iterative, options: .repeating)
            .accessibilityLabel("Working")
    }
}
