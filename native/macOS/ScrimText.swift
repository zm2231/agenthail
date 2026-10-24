import SwiftUI

extension View {
    func scrimText() -> some View {
        modifier(ScrimText())
    }

    func scrimSurface(radius: CGFloat) -> some View {
        modifier(ScrimSurface(radius: radius))
    }
}

private struct ScrimText: ViewModifier {
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if backdrop != nil, let anchor = place.anchor {
            content.background { ScrimRegion(anchor: anchor).allowsHitTesting(false).accessibilityHidden(true) }
        } else {
            content
        }
    }
}

private struct ScrimSurface: ViewModifier {
    let radius: CGFloat
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if let backdrop {
            content.background { CardLayer(backdrop: backdrop, place: place, radius: radius) }
        } else {
            content
        }
    }
}

private struct CardLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let place: BackdropPlace
    let radius: CGFloat

    var body: some View {
        if backdrop.active, let layers = backdrop.layers {
            BackdropLayer(style: BackdropLayerStyle(kind: .card, role: place.role, radius: radius, clipsToAnchor: place.role != nil), set: layers, anchor: place.anchor)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        }
    }
}
