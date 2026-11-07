@testable import AgenthailHelpers
import AppKit
import Foundation

@main
@MainActor
struct TranscriptClipTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    final class Document: NSView {
        override var isFlipped: Bool { true }
    }

    static func main() {
        let scroll = NSScrollView(frame: NSRect(x: 0, y: 0, width: 400, height: 600))
        scroll.automaticallyAdjustsContentInsets = false
        scroll.contentInsets = NSEdgeInsets(top: 50, left: 0, bottom: 12, right: 0)
        let document = Document(frame: NSRect(x: 0, y: 0, width: 400, height: 2000))
        scroll.documentView = document
        let clip = TranscriptClip()
        check(clip.distanceToEnd == nil, "a clip with no scroll view reads nothing")
        clip.scrollToEnd()

        let anchor = TranscriptClipAnchor.AnchorView(frame: .zero)
        anchor.clip = clip
        document.addSubview(anchor)
        anchor.register()

        let end = 2000 + 12 - scroll.contentView.bounds.height
        scroll.contentView.scroll(to: NSPoint(x: 0, y: end - 44))
        check(abs((clip.distanceToEnd ?? 0) - 44) < 0.5, "the shortfall reads the clip short by the growth, read \(String(describing: clip.distanceToEnd))")
        clip.scrollToEnd()
        check(abs(clip.distanceToEnd ?? 1) < 0.5, "moving to the end leaves no shortfall, read \(String(describing: clip.distanceToEnd))")
        check(abs(scroll.contentView.bounds.maxY - 2012) < 0.5, "the end sits below the bottom inset and no further, at \(scroll.contentView.bounds.maxY)")
        clip.scrollToEnd()
        check(abs(scroll.contentView.bounds.maxY - 2012) < 0.5, "a second move stays at the end")

        let other = NSScrollView(frame: NSRect(x: 0, y: 0, width: 400, height: 600))
        other.documentView = Document(frame: NSRect(x: 0, y: 0, width: 400, height: 900))
        anchor.removeFromSuperview()
        other.documentView?.addSubview(anchor)
        anchor.register()
        check(abs((clip.distanceToEnd ?? 0) - (900 - other.contentView.bounds.maxY)) < 0.5, "an anchor moved into another transcript reads that transcript's clip")
        print("ok   transcript_clip_test")
    }
}
