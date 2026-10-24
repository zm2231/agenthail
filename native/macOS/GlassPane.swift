import SwiftUI

extension View {
    func glassPane(_ role: WindowBackdrop.PaneRole, fallback: Color, edge: VerticalEdge? = nil, gaps: Edge.Set = .all) -> some View {
        modifier(GlassPane(role: role, fallback: fallback, edge: edge, gaps: gaps))
    }
}

private struct GlassPane: ViewModifier {
    let role: WindowBackdrop.PaneRole
    let fallback: Color
    let edge: VerticalEdge?
    let gaps: Edge.Set
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if let backdrop, role == .chrome {
            ChromeBody(content: content, backdrop: backdrop, place: place, fallback: fallback, edge: edge)
        } else if let backdrop, let layer = role.layer {
            GlassPaneBody(content: content, role: role, layer: layer, fallback: fallback, gaps: gaps, backdrop: backdrop)
        } else {
            content.background(fallback)
        }
    }
}

private struct ChromeBody<Content: View>: View {
    let content: Content
    @ObservedObject var backdrop: WindowBackdrop
    let place: BackdropPlace
    let fallback: Color
    let edge: VerticalEdge?

    var body: some View {
        if backdrop.active, backdrop.solid, backdrop.gutter > 0, edge == .top {
            let shape = RoundedRectangle(cornerRadius: backdrop.paneRadius)
            content
                .background {
                    ChromeLayer(backdrop: backdrop, place: place, fallback: fallback, edge: nil)
                        .clipShape(shape)
                        .overlay(shape.strokeBorder(DesktopPalette.line, lineWidth: 1))
                }
                .padding([.horizontal, .bottom], backdrop.gutter)
        } else {
            content.background {
                ChromeLayer(backdrop: backdrop, place: place, fallback: fallback, edge: edge).ignoresSafeArea()
            }
        }
    }
}

private struct ChromeLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let place: BackdropPlace
    let fallback: Color
    let edge: VerticalEdge?

    var body: some View {
        if backdrop.active, let layers = backdrop.layers, let anchor = place.anchor {
            ZStack(alignment: edge == .top ? .top : .bottom) {
                BackdropLayer(style: BackdropLayerStyle(kind: .surface, tint: backdrop.tints[.chrome] ?? 0), set: layers, anchor: anchor)
                ScrimRegion(anchor: anchor)
                if backdrop.solid, edge != nil { Rectangle().fill(DesktopPalette.line).frame(height: 1) }
            }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        } else {
            fallback
        }
    }
}

private struct GlassPaneBody<Content: View>: View {
    let content: Content
    let role: WindowBackdrop.PaneRole
    let layer: BackdropRole
    let fallback: Color
    let gaps: Edge.Set
    @ObservedObject var backdrop: WindowBackdrop
    @State private var anchor = ScrimAnchor()

    var body: some View {
        let insets = backdrop.paneInsets(gaps)
        let shape = RoundedRectangle(cornerRadius: backdrop.paneRadius)
        content
            .environment(\.backdropPlace, BackdropPlace(role: layer, anchor: anchor))
            .padding(insets)
            .background {
                ZStack {
                    if backdrop.active, let layers = backdrop.layers {
                        BackdropLayer(style: BackdropLayerStyle(kind: .surface, role: layer, tint: backdrop.tints[role] ?? 0, radius: backdrop.paneRadius, hostsAnchor: true), set: layers, anchor: anchor)
                    } else {
                        fallback
                    }
                    if backdrop.active, backdrop.solid {
                        GlassColumnMask(radius: backdrop.paneRadius)
                    }
                    if backdrop.active, backdrop.border > 0 || backdrop.solid {
                        shape.strokeBorder(backdrop.border > 0 ? AnyShapeStyle(DesktopPalette.text.opacity(backdrop.border)) : AnyShapeStyle(DesktopPalette.line), lineWidth: 1)
                    }
                }
                .padding(insets)
                .allowsHitTesting(false)
                .accessibilityHidden(true)
                .ignoresSafeArea(edges: backdrop.gutter > 0 ? [] : .all)
            }
    }
}
