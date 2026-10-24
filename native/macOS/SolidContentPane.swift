import SwiftUI

extension View {
    func solidContentPane() -> some View {
        modifier(SolidContentPane())
    }
}

private struct SolidContentPane: ViewModifier {
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            SolidContentPaneBody(content: content, backdrop: backdrop)
        } else {
            content
        }
    }
}

private struct SolidContentPaneBody<Content: View>: View {
    let content: Content
    @ObservedObject var backdrop: WindowBackdrop

    var body: some View {
        if backdrop.solid {
            content.glassPane(.content, fallback: DesktopPalette.window)
        } else {
            content
        }
    }
}
