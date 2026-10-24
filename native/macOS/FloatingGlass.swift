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
        if !backdrop.active || reduceTransparency {
            content.background(fallback, in: shape)
        } else if #available(macOS 26, *) {
            content
                .background(DesktopPalette.window.opacity(backdrop.composerOpacity), in: shape)
                .glassEffect(.regular, in: shape)
        } else {
            content
                .background(DesktopPalette.window.opacity(backdrop.composerOpacity), in: shape)
                .background(.ultraThinMaterial, in: shape)
        }
    }
}
