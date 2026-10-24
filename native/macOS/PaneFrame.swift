import SwiftUI

extension WindowBackdrop {
    var frameGutter: CGFloat { solid ? gutter : gutter / 2 }

    func paneInsets(_ gaps: Edge.Set) -> EdgeInsets {
        let open: Edge.Set = solid ? gaps : .all
        func side(_ edge: Edge.Set) -> CGFloat { open.contains(edge) ? frameGutter : 0 }
        return EdgeInsets(top: side(.top), leading: side(.leading), bottom: side(.bottom), trailing: side(.trailing))
    }
}

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
        content.padding(edges, backdrop.frameGutter)
    }
}
