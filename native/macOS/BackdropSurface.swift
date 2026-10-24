import SwiftUI

extension View {
    func backdropSurface(_ fallback: Color) -> some View {
        modifier(BackdropSurface(fallback: fallback, fade: nil))
    }

    func backdropFade(_ fallback: Color, solidFrom: CGFloat) -> some View {
        modifier(BackdropSurface(fallback: fallback, fade: solidFrom))
    }
}

private struct BackdropSurface: ViewModifier {
    let fallback: Color
    let fade: CGFloat?
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        content.background {
            Group {
                if let backdrop {
                    SurfaceLayer(backdrop: backdrop, place: place, fallback: fallback, fade: fade)
                } else {
                    Fallback(color: fallback, fade: fade)
                }
            }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        }
    }
}

private struct SurfaceLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let place: BackdropPlace
    let fallback: Color
    let fade: CGFloat?

    var body: some View {
        if backdrop.active, let layers = backdrop.layers {
            let tint = place.role.map { backdrop.tints[$0 == .sidebar ? .sidebar : .content] ?? 0 } ?? 0
            BackdropLayer(style: BackdropLayerStyle(kind: .surface, role: place.role, tint: tint, fade: fade, clipsToAnchor: place.role != nil), set: layers, anchor: place.anchor)
        } else {
            Fallback(color: fallback, fade: fade)
        }
    }
}

private struct Fallback: View {
    let color: Color
    let fade: CGFloat?

    var body: some View {
        if let fade {
            color.mask(LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: UnitPoint(x: 0.5, y: fade)))
        } else {
            color
        }
    }
}
