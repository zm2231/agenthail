import SwiftUI

extension View {
    func floatingEdge<S: InsettableShape>(_ shape: S, highlight: AnyShapeStyle? = nil) -> some View {
        modifier(FloatingEdge(shape: shape, highlight: highlight))
    }
}

private struct FloatingEdge<S: InsettableShape>: ViewModifier {
    let shape: S
    let highlight: AnyShapeStyle?
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            ObservedEdge(content: content, shape: shape, highlight: highlight, backdrop: backdrop)
        } else {
            content.modifier(Edge(shape: shape, highlight: highlight, raised: false))
        }
    }
}

private struct ObservedEdge<Content: View, S: InsettableShape>: View {
    let content: Content
    let shape: S
    let highlight: AnyShapeStyle?
    @ObservedObject var backdrop: WindowBackdrop

    var body: some View {
        content.modifier(Edge(shape: shape, highlight: highlight, raised: backdrop.active && backdrop.solid))
    }
}

private struct Edge<S: InsettableShape>: ViewModifier {
    let shape: S
    let highlight: AnyShapeStyle?
    let raised: Bool

    func body(content: Content) -> some View {
        content
            .overlay(shape.strokeBorder(highlight ?? AnyShapeStyle(DesktopPalette.line), lineWidth: highlight == nil ? 1 : 2))
            .shadow(color: .black.opacity(raised ? 0.12 : 0.08), radius: raised ? 16 : 12, y: 6)
    }
}
