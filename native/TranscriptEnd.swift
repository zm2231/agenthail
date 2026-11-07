import SwiftUI

// containerSize excludes both content insets while visibleRect spans them, so the end is measured from visibleRect.
struct TranscriptEnd: Equatable {
    let contentHeight: CGFloat
    let visibleBottom: CGFloat
    let bottomInset: CGFloat

    init(_ geometry: ScrollGeometry) {
        contentHeight = geometry.contentSize.height
        visibleBottom = geometry.visibleRect.maxY
        bottomInset = geometry.contentInsets.bottom
    }

    var distance: CGFloat { contentHeight - (visibleBottom - bottomInset) }
}
