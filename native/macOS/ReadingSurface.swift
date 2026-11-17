import SwiftUI

enum ReadingSurface {
    static let radius: CGFloat = 14

    static func width(pane: CGFloat) -> CGFloat {
        min(pane, DesktopColumns.transcript(pane: pane).column + 2 * DesktopColumns.gutter)
    }
}

extension View {
    func readingSurface(below top: CGFloat) -> some View {
        modifier(ReadingSurfaceBand(top: top))
    }

    func readingSurfaceFloor(_ inset: CGFloat) -> some View {
        modifier(ReadingSurfaceFloor(inset: inset))
    }
}

private struct ReadingSurfaceFloor: ViewModifier {
    let inset: CGFloat
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if let backdrop {
            ReadingSurfaceFloorPad(backdrop: backdrop, anchor: place.anchor, inset: inset) { content }
        } else {
            content.padding(.bottom, inset).frame(maxWidth: .infinity).backdropFade(DesktopPalette.window, solidFrom: 0.38)
        }
    }
}

private struct ReadingSurfaceFloorPad<Content: View>: View {
    @ObservedObject var backdrop: WindowBackdrop
    let anchor: ScrimAnchor?
    let inset: CGFloat
    @ViewBuilder let content: Content

    var body: some View {
        if backdrop.active, !backdrop.solid, let layers = backdrop.layers {
            let tint = layers.layers.panes[.reading]?.even ?? Float(backdrop.tints[.content] ?? 0)
            content
                .padding(.bottom, DesktopColumns.gutter)
                .background {
                    BackdropLayer(style: BackdropLayerStyle(kind: .surface, role: .reading, tint: CGFloat(tint), radius: ReadingSurface.radius, fade: 0.38), set: layers, anchor: anchor)
                        .allowsHitTesting(false)
                        .accessibilityHidden(true)
                }
                .frame(maxWidth: .infinity)
        } else {
            content.padding(.bottom, inset).frame(maxWidth: .infinity).backdropFade(DesktopPalette.window, solidFrom: 0.38)
        }
    }
}

private struct ReadingSurfaceBand: ViewModifier {
    let top: CGFloat
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        content.background(alignment: .top) {
            if let backdrop {
                GeometryReader { proxy in
                    ReadingSurfaceLayer(backdrop: backdrop, anchor: place.anchor)
                        .frame(width: ReadingSurface.width(pane: proxy.size.width), height: max(proxy.size.height - top, 0))
                        .frame(maxWidth: .infinity)
                        .padding(.top, top)
                }
                .ignoresSafeArea(.container, edges: .bottom)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
            }
        }
    }
}

private struct ReadingSurfaceLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let anchor: ScrimAnchor?

    var body: some View {
        if backdrop.active, !backdrop.solid, let layers = backdrop.layers {
            let tint = layers.layers.panes[.reading]?.even ?? Float(backdrop.tints[.content] ?? 0)
            BackdropLayer(style: BackdropLayerStyle(kind: .surface, role: .reading, tint: CGFloat(tint), radius: ReadingSurface.radius), set: layers, anchor: anchor)
        }
    }
}
