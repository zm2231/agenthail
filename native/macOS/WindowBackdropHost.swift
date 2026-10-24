import AppKit
import SwiftUI

struct WindowBackdropHost: ViewModifier {
    @ObservedObject var interface: InterfaceStore
    @StateObject private var backdrop = WindowBackdrop()
    @State private var anchor = ScrimAnchor()

    func body(content: Content) -> some View {
        content
            .background {
                ZStack {
                    if backdrop.desktop { DesktopBlur() }
                    WindowLayer(backdrop: backdrop, anchor: anchor)
                    WindowRectReader { _, window in backdrop.windowChanged(window) }
                }
                .ignoresSafeArea()
            }
            .environment(\.windowBackdrop, backdrop)
            .environment(\.backdropPlace, BackdropPlace(role: nil, anchor: anchor))
            .onAppear { backdrop.configure(interface.config, wallpapers: interface.wallpapers) }
            .onChange(of: interface.config) { backdrop.configure(interface.config, wallpapers: interface.wallpapers) }
            .onChange(of: interface.wallpapers) { backdrop.configure(interface.config, wallpapers: interface.wallpapers) }
    }
}

extension View {
    func windowBackdrop(_ interface: InterfaceStore) -> some View {
        modifier(WindowBackdropHost(interface: interface))
    }
}

private struct WindowLayer: View {
    @ObservedObject var backdrop: WindowBackdrop
    let anchor: ScrimAnchor

    var body: some View {
        if backdrop.active, let layers = backdrop.layers {
            BackdropLayer(style: BackdropLayerStyle(kind: .surface, hostsAnchor: true), set: layers, anchor: anchor)
                .onAppear { anchor.feather = backdrop.feather }
                .onChange(of: backdrop.feather) {
                    anchor.feather = backdrop.feather
                    anchor.invalidate()
                }
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        } else {
            DesktopPalette.window.opacity(backdrop.desktop ? 0 : 1)
        }
    }
}

private struct DesktopBlur: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()
        view.blendingMode = .behindWindow
        view.material = .underWindowBackground
        view.state = .active
        return view
    }

    func updateNSView(_ view: NSVisualEffectView, context: Context) {}
}
