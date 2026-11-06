import SwiftUI

extension View {
    func transcriptScrimSource() -> some View {
        modifier(TranscriptScrimSource())
    }
}

private struct TranscriptScrimSource: ViewModifier {
    @Environment(\.windowBackdrop) private var backdrop
    @Environment(\.backdropPlace) private var place

    func body(content: Content) -> some View {
        if let backdrop, let anchor = place.anchor {
            TranscriptScrimSourceBody(content: content, backdrop: backdrop, anchor: anchor)
        } else {
            content
        }
    }
}

private struct TranscriptScrimSourceBody<Content: View>: View {
    let content: Content
    @ObservedObject var backdrop: WindowBackdrop
    let anchor: ScrimAnchor

    var body: some View {
        content.environment(\.transcriptScrim, backdrop.active ? anchor.transcript : nil)
    }
}
