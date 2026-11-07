import AppKit
import SwiftUI

@MainActor
final class TranscriptClip {
    fileprivate weak var scroll: NSScrollView?

    var distanceToEnd: CGFloat? {
        guard let scroll, let document = scroll.documentView else { return nil }
        let clip = scroll.contentView
        return document.frame.height + clip.contentInsets.bottom - clip.bounds.maxY
    }

    func scrollToEnd() {
        guard let scroll, let document = scroll.documentView else { return }
        let clip = scroll.contentView
        clip.scroll(to: NSPoint(x: clip.bounds.minX, y: document.frame.height + clip.contentInsets.bottom - clip.bounds.height))
        scroll.reflectScrolledClipView(clip)
    }
}

extension EnvironmentValues {
    @Entry var transcriptClip: TranscriptClip?
}

/// Zero-size, inside the transcript's scroll content, so its enclosing scroll view is the transcript's.
struct TranscriptClipAnchor: NSViewRepresentable {
    @Environment(\.transcriptClip) private var clip

    func makeNSView(context: Context) -> AnchorView { AnchorView() }

    func updateNSView(_ view: AnchorView, context: Context) {
        view.clip = clip
        view.register()
    }

    final class AnchorView: NSView {
        weak var clip: TranscriptClip?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            register()
        }

        func register() {
            guard let clip, let scroll = enclosingScrollView, clip.scroll !== scroll else { return }
            clip.scroll = scroll
        }

        override func hitTest(_ point: NSPoint) -> NSView? { nil }
    }
}
