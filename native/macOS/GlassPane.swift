import SwiftUI

extension View {
    func glassPane(_ role: WindowBackdrop.PaneRole, fallback: Color) -> some View {
        modifier(GlassPane(role: role, fallback: fallback))
    }
}

private struct GlassPane: ViewModifier {
    let role: WindowBackdrop.PaneRole
    let fallback: Color
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if let backdrop, role == .chrome {
            content.background {
                ChromeLayer(backdrop: backdrop, place: place, fallback: fallback).ignoresSafeArea()
            }
        } else if let backdrop, let layer = role.layer {
            GlassPaneBody(content: content, role: role, layer: layer, fallback: fallback, backdrop: backdrop)
        } else {
            content.background(fallback)
        }
    }
}

private struct ChromeLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let place: BackdropPlace
    let fallback: Color

    var body: some View {
        if backdrop.active, let layers = backdrop.layers, let anchor = place.anchor {
            ZStack {
                BackdropLayer(style: BackdropLayerStyle(kind: .surface, tint: backdrop.tints[.chrome] ?? 0), set: layers, anchor: anchor)
                ScrimRegion(anchor: anchor)
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
    @ObservedObject var backdrop: WindowBackdrop
    @State private var anchor = ScrimAnchor()

    var body: some View {
        content
            .environment(\.backdropPlace, BackdropPlace(role: layer, anchor: anchor))
            .padding(backdrop.gutter)
            .background {
                ZStack {
                    if backdrop.active, let layers = backdrop.layers {
                        BackdropLayer(style: BackdropLayerStyle(kind: .surface, role: layer, tint: backdrop.tints[role] ?? 0, inset: backdrop.gutter, radius: backdrop.paneRadius, hostsAnchor: true), set: layers, anchor: anchor)
                    } else {
                        fallback
                    }
                    if backdrop.active, backdrop.border > 0 {
                        RoundedRectangle(cornerRadius: backdrop.paneRadius)
                            .strokeBorder(DesktopPalette.text.opacity(backdrop.border), lineWidth: 1)
                            .padding(backdrop.gutter)
                    }
                }
                .allowsHitTesting(false)
                .accessibilityHidden(true)
                .ignoresSafeArea()
            }
    }
}
