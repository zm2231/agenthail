import SwiftUI

/// A list card shows its slice of the card layer, so it is blurred and
/// darkened only as much as its text needs where it sits on screen. On a
/// reading pane, rows only deepen the pane, which never lowers contrast.
struct GlassRowBackground: View {
    let selected: Bool
    @Environment(\.cardBackdrop) private var cards
    @Environment(\.phoneAppearance) private var appearance
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        ZStack {
            if reduceTransparency || appearance?.active != true {
                Color(uiColor: .secondarySystemGroupedBackground)
            } else if let cards {
                if let image = cards.image {
                    CardSlice(image: image, layer: cards.frame)
                } else {
                    Rectangle().fill(.ultraThinMaterial)
                    PhonePalette.ground.opacity(appearance?.config.sidebarOpacity ?? 1)
                }
            } else {
                PhonePalette.ground.opacity(0.35)
            }
            if selected { Color.accentColor.opacity(0.16) }
        }
    }
}

private struct CardSlice: View {
    let image: BackdropImage
    let layer: CGRect

    var body: some View {
        GeometryReader { proxy in
            let origin = proxy.frame(in: .global).origin
            Image(decorative: image.image, scale: CGFloat(image.image.width) / max(image.size.width, 1))
                .resizable()
                .frame(width: layer.width, height: layer.height)
                .offset(x: layer.minX - origin.x, y: layer.minY - origin.y)
        }
        .clipped()
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

extension View {
    func glassLabel() -> some View {
        modifier(GlassLabel())
    }
}

private struct GlassLabel: ViewModifier {
    @Environment(\.cardBackdrop) private var cards

    func body(content: Content) -> some View {
        if cards == nil {
            content
        } else {
            content
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .floatingGlass(in: Capsule())
        }
    }
}
