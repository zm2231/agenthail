import SwiftUI

struct WorkingIndicator: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let preview = ProcessInfo.processInfo.arguments.contains("--preview-session")
        Image(systemName: "circle.dotted")
            .font(.system(size: 17)).foregroundStyle(SessionStyle.accent)
            .symbolEffect(.rotate, options: .repeating, isActive: !reduceMotion && !preview)
            .accessibilityLabel("Agent working")
    }
}
