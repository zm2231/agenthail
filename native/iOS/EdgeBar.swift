import SwiftUI

extension View {
    /// Floating controls over the edge of a scroll view; content fades out
    /// under them instead of showing through behind their text.
    func edgeBar<Bar: View>(_ edge: VerticalEdge, spacing: CGFloat? = nil, @ViewBuilder content: () -> Bar) -> some View {
        modifier(EdgeBar(edge: edge, spacing: spacing, bar: content()))
    }
}

private struct EdgeBar<Bar: View>: ViewModifier {
    let edge: VerticalEdge
    let spacing: CGFloat?
    let bar: Bar

    func body(content: Content) -> some View {
        if #available(iOS 26, *) {
            content
                .safeAreaBar(edge: edge, spacing: spacing) { bar }
                .scrollEdgeEffectStyle(.soft, for: edge == .top ? .top : .bottom)
        } else {
            content.safeAreaInset(edge: edge, spacing: spacing) {
                bar.background {
                    Rectangle().fill(.ultraThinMaterial)
                        .mask(LinearGradient(colors: [.black, .black.opacity(0)], startPoint: edge == .top ? .top : .bottom, endPoint: edge == .top ? .bottom : .top))
                        .ignoresSafeArea(edges: edge == .top ? .top : .bottom)
                }
            }
        }
    }
}
