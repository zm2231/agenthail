import SwiftUI

extension View {
    /// Content under the top bar is covered, since header text sits on it; under the bottom bar it fades.
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
                .scrollEdgeEffectStyle(edge == .top ? .hard : .soft, for: edge == .top ? .top : .bottom)
        } else {
            content.safeAreaInset(edge: edge, spacing: spacing) {
                bar.background {
                    if edge == .top {
                        Rectangle().fill(.bar).ignoresSafeArea(edges: .top)
                    } else {
                        Rectangle().fill(.ultraThinMaterial)
                            .mask(LinearGradient(colors: [.black, .black.opacity(0)], startPoint: .bottom, endPoint: .top))
                            .ignoresSafeArea(edges: .bottom)
                    }
                }
            }
        }
    }
}
