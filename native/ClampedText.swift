import SwiftUI

struct ClampedText: View {
    let text: Text
    let lines: Int
    @Binding var truncated: Bool
    @State private var full: CGFloat = 0
    @State private var clamped: CGFloat = 0

    var body: some View {
        text
            .lineLimit(lines)
            .frame(maxWidth: .infinity, alignment: .leading)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { clamped = $0; measure() }
            .background(alignment: .topLeading) {
                text
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .hidden()
                    .accessibilityHidden(true)
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { full = $0; measure() }
            }
    }

    private func measure() {
        let overflow = full > clamped + 1
        if overflow != truncated { truncated = overflow }
    }
}
