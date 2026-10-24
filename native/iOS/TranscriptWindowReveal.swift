import SwiftUI

struct TranscriptWindowReveal: ViewModifier {
    @Binding var window: TranscriptWindow
    let ids: [String]

    struct Reach: Equatable {
        var nearTop: Bool
        var atBottom: Bool
        var underfilled: Bool
        var height: CGFloat

        init(_ geometry: ScrollGeometry) {
            nearTop = geometry.contentOffset.y + geometry.contentInsets.top < geometry.containerSize.height
            atBottom = geometry.contentSize.height - geometry.visibleRect.maxY < 60
            underfilled = geometry.contentSize.height < geometry.containerSize.height
            height = geometry.contentSize.height
        }
    }

    func body(content: Content) -> some View {
        content.onScrollGeometryChange(for: Reach.self, of: Reach.init) { _, reach in
            guard window.hidesOlder(in: ids), reach.underfilled || (reach.nearTop && !reach.atBottom) else { return }
            window.revealOlder(in: ids)
        }
    }
}

extension View {
    func transcriptWindow(_ window: Binding<TranscriptWindow>, ids: [String]) -> some View {
        modifier(TranscriptWindowReveal(window: window, ids: ids))
    }
}
