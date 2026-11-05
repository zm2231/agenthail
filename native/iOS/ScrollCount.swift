#if DEBUG
import SwiftUI

@MainActor
enum ScrollCount {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--scroll-count")
    static var value = 0
}

struct ScrollCountLabel: View {
    var body: some View {
        if ScrollCount.enabled {
            TimelineView(.periodic(from: .now, by: 0.25)) { _ in
                Text("\(ScrollCount.value)")
                    .font(.system(size: 1)).opacity(0.01)
                    .frame(width: 1, height: 1).clipped()
                    .accessibilityIdentifier("scroll-count")
            }
        }
    }
}
#endif
