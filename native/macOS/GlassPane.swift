import SwiftUI

extension View {
    func glassPane(_ role: WindowBackdrop.PaneRole, fallback: Color, edge: VerticalEdge? = nil, gaps: Edge.Set = .all, enabled: Bool = true) -> some View {
        modifier(GlassPane(role: role, fallback: fallback, edge: edge, gaps: gaps, enabled: enabled))
    }
}

private struct GlassPane: ViewModifier {
    let role: WindowBackdrop.PaneRole
    let fallback: Color
    let edge: VerticalEdge?
    let gaps: Edge.Set
    let enabled: Bool
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if let backdrop, role == .chrome {
            ChromeBody(content: content, backdrop: backdrop, place: place, fallback: fallback, edge: edge)
        } else if let backdrop, let layer = role.layer {
            GlassPaneBody(content: content, role: role, layer: layer, fallback: fallback, gaps: gaps, enabled: enabled, inherited: place, backdrop: backdrop)
        } else {
            content.background(enabled ? fallback : .clear)
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
        let card = backdrop.active && backdrop.solid && backdrop.gutter > 0 && edge == .top
        let shape = RoundedRectangle(cornerRadius: backdrop.paneRadius)
        content
            .background {
                if card {
                    ChromeLayer(backdrop: backdrop, place: place, fallback: fallback, edge: nil)
                        .clipShape(shape)
                        .overlay(shape.strokeBorder(DesktopPalette.line, lineWidth: 1))
                } else {
                    ChromeLayer(backdrop: backdrop, place: place, fallback: fallback, edge: edge).ignoresSafeArea()
                }
            }
            .padding([.horizontal, .bottom], card ? backdrop.gutter : 0)
    }
}

private struct ChromeLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let place: BackdropPlace
    let fallback: Color
    let edge: VerticalEdge?

    var body: some View {
        ZStack(alignment: edge == .top ? .top : .bottom) {
            if backdrop.active, let layers = backdrop.layers, let anchor = place.anchor {
                BackdropLayer(style: BackdropLayerStyle(kind: .surface, tint: backdrop.tints[.chrome] ?? 0), set: layers, anchor: anchor)
                ScrimRegion(anchor: anchor)
            } else {
                fallback
            }
            if edge != nil { Rectangle().fill(DesktopPalette.line).frame(height: 1) }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

private struct GlassPaneBody<Content: View>: View {
    let content: Content
    let role: WindowBackdrop.PaneRole
    let layer: BackdropRole
    let fallback: Color
    let gaps: Edge.Set
    let enabled: Bool
    let inherited: BackdropPlace
    @ObservedObject var backdrop: WindowBackdrop
    @State private var anchor = ScrimAnchor()

    var body: some View {
        let insets = enabled ? backdrop.paneInsets(gaps) : EdgeInsets()
        let shape = RoundedRectangle(cornerRadius: backdrop.paneRadius)
        content
            .environment(\.backdropPlace, enabled ? BackdropPlace(role: layer, anchor: anchor) : inherited)
            .padding(insets)
            .background {
                if enabled {
                    ZStack {
                        if backdrop.active, let layers = backdrop.layers {
                            BackdropLayer(style: BackdropLayerStyle(kind: .surface, role: layer, tint: backdrop.tints[role] ?? 0, radius: backdrop.paneRadius, hostsAnchor: true), set: layers, anchor: anchor)
                        } else {
                            fallback
                        }
                        if backdrop.active, backdrop.solid {
                            GlassColumnMask(radius: backdrop.paneRadius)
                        }
                        if backdrop.active, backdrop.gutter > 0, backdrop.border > 0 || backdrop.solid {
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
}
