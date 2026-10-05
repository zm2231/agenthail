import SwiftUI

struct BoundedDock<Content: View>: View {
    static var defaultMaxHeight: CGFloat { 200 }

    var maxHeight: CGFloat = defaultMaxHeight
    @ViewBuilder let content: Content

    var body: some View {
        HeightCap(maxHeight: maxHeight) {
            ScrollView { content }
                .scrollBounceBehavior(.basedOnSize)
        }
    }
}

private struct HeightCap: Layout {
    let maxHeight: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let child = subviews.first else { return .zero }
        let ideal = child.sizeThatFits(ProposedViewSize(width: proposal.width, height: nil))
        return CGSize(width: proposal.width ?? ideal.width, height: min(ideal.height, maxHeight, proposal.height ?? .infinity))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, anchor: .topLeading, proposal: ProposedViewSize(bounds.size))
    }
}
