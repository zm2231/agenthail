import SwiftUI

extension View {
    func floatingGlass<S: InsettableShape>(_ fallback: Color, in shape: S) -> some View {
        modifier(FloatingGlass(fallback: fallback, shape: shape))
    }
}

private struct FloatingGlass<S: InsettableShape>: ViewModifier {
    let fallback: Color
    let shape: S
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            FloatingGlassBody(content: content, fallback: fallback, shape: shape, backdrop: backdrop)
        } else {
            content.background(fallback, in: shape)
        }
    }
}

private struct FloatingGlassBody<Content: View, S: InsettableShape>: View {
    let content: Content
    let fallback: Color
    let shape: S
    @ObservedObject var backdrop: WindowBackdrop
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        let glass = backdrop.active && !reduceTransparency && !backdrop.solid
        let tinted = content.background(glass ? DesktopPalette.window.opacity(backdrop.composerOpacity) : fallback, in: shape)
        if #available(macOS 26, *) {
            tinted.glassEffect(glass ? .regular : .identity, in: shape)
        } else {
            tinted.background { if glass { shape.fill(.ultraThinMaterial) } }
        }
    }
}
