import SwiftUI

struct ResponsivePairLayout: Layout {
    let spacing: CGFloat
    let minimumLeadingWidth: CGFloat
    let minimumTrailingWidth: CGFloat

    private func isHorizontal(_ width: CGFloat?) -> Bool {
        guard let width else { return true }
        return width >= minimumLeadingWidth + minimumTrailingWidth + spacing
    }

    static func horizontalWidths(
        available: CGFloat,
        spacing: CGFloat,
        minimumLeadingWidth: CGFloat,
        minimumTrailingWidth: CGFloat
    ) -> (leading: CGFloat, trailing: CGFloat) {
        let remaining = max(0, available - spacing - minimumLeadingWidth - minimumTrailingWidth)
        let leading = minimumLeadingWidth + remaining / 2
        return (leading, minimumTrailingWidth + remaining - remaining / 2)
    }

    private func horizontalWidths(for width: CGFloat) -> (leading: CGFloat, trailing: CGFloat) {
        Self.horizontalWidths(
            available: width,
            spacing: spacing,
            minimumLeadingWidth: minimumLeadingWidth,
            minimumTrailingWidth: minimumTrailingWidth
        )
    }

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard subviews.count == 2 else { return .zero }
        if isHorizontal(proposal.width) {
            let width = proposal.width ?? minimumLeadingWidth + minimumTrailingWidth + spacing
            let columns = horizontalWidths(for: width)
            let leading = subviews[0].sizeThatFits(ProposedViewSize(width: columns.leading, height: proposal.height))
            let trailing = subviews[1].sizeThatFits(ProposedViewSize(width: columns.trailing, height: proposal.height))
            return CGSize(width: proposal.width ?? leading.width + spacing + trailing.width, height: max(leading.height, trailing.height))
        }
        let childProposal = ProposedViewSize(width: proposal.width, height: nil)
        let leading = subviews[0].sizeThatFits(childProposal)
        let trailing = subviews[1].sizeThatFits(childProposal)
        return CGSize(width: proposal.width ?? max(leading.width, trailing.width), height: leading.height + spacing + trailing.height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        guard subviews.count == 2 else { return }
        if isHorizontal(bounds.width) {
            let columns = horizontalWidths(for: bounds.width)
            subviews[0].place(at: bounds.origin, anchor: .topLeading, proposal: ProposedViewSize(width: columns.leading, height: bounds.height))
            subviews[1].place(at: CGPoint(x: bounds.minX + columns.leading + spacing, y: bounds.minY), anchor: .topLeading, proposal: ProposedViewSize(width: columns.trailing, height: bounds.height))
            return
        }
        let childProposal = ProposedViewSize(width: bounds.width, height: nil)
        let leading = subviews[0].sizeThatFits(childProposal)
        subviews[0].place(at: bounds.origin, anchor: .topLeading, proposal: childProposal)
        subviews[1].place(at: CGPoint(x: bounds.minX, y: bounds.minY + leading.height + spacing), anchor: .topLeading, proposal: childProposal)
    }
}
