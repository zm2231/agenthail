import SwiftUI

extension View {
    func solidContentPane(gaps: Edge.Set) -> some View {
        modifier(SolidContentPane(gaps: gaps))
    }
}

private struct SolidContentPane: ViewModifier {
    let gaps: Edge.Set
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            SolidContentPaneBody(content: content, gaps: gaps, backdrop: backdrop)
        } else {
            content
        }
    }
}

private struct SolidContentPaneBody<Content: View>: View {
    let content: Content
    let gaps: Edge.Set
    @ObservedObject var backdrop: WindowBackdrop

    var body: some View {
        if backdrop.solid {
            content.glassPane(.content, fallback: DesktopPalette.window, gaps: gaps)
        } else {
            content
        }
    }
}
