import SwiftUI

extension View {
    func paneFrame(_ edges: Edge.Set) -> some View {
        modifier(PaneFrame(edges: edges))
    }
}

private struct PaneFrame: ViewModifier {
    let edges: Edge.Set
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            PaneFrameBody(content: content, edges: edges, backdrop: backdrop)
        } else {
            content
        }
    }
}

private struct PaneFrameBody<Content: View>: View {
    let content: Content
    let edges: Edge.Set
    @ObservedObject var backdrop: WindowBackdrop

    var body: some View {
        content.padding(edges, backdrop.gutter / 2)
    }
}
