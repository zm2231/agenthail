import AppKit
import SwiftUI

@MainActor
final class TranscriptClip {
    fileprivate weak var scroll: NSScrollView? {
        didSet { if scroll !== oldValue { watch() } }
    }
    // Runs before AppKit moves the clip, so no layout pass can pin the reader back first.
    var readerLeaves: (@MainActor () -> Void)?
    private var monitor: EventMonitor?

    private func watch() {
        monitor = scroll == nil ? nil : EventMonitor { [weak self] event in
            guard let self, let window = event.window else { return }
            self.receive(event, in: window)
        }
    }

    func receive(_ event: NSEvent, in window: NSWindow) {
        if movesAway(event, in: window) { readerLeaves?() }
    }

    func movesAway(_ event: NSEvent, in window: NSWindow) -> Bool {
        guard let scroll, scroll.window === window else { return false }
        let point = scroll.convert(event.locationInWindow, from: nil)
        switch event.type {
        case .scrollWheel:
            return event.scrollingDeltaY > 0 && scroll.bounds.contains(point) && window.contentView?.hitTest(event.locationInWindow).map { $0 === scroll || $0.isDescendant(of: scroll) } == true
        case .keyDown:
            guard let responder = window.firstResponder as? NSView, responder === scroll || responder.isDescendant(of: scroll) else { return false }
            return [115, 116].contains(event.keyCode)
        case .leftMouseDown:
            guard let scroller = scroll.verticalScroller, !scroller.isHidden else { return false }
            return scroller.bounds.contains(scroller.convert(event.locationInWindow, from: nil))
        default:
            return false
        }
    }

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

private final class EventMonitor {
    private var token: Any?

    @MainActor
    init(_ handle: @escaping @MainActor (NSEvent) -> Void) {
        token = NSEvent.addLocalMonitorForEvents(matching: [.scrollWheel, .keyDown, .leftMouseDown]) { event in
            MainActor.assumeIsolated { handle(event) }
            return event
        }
    }

    deinit {
        if let token { NSEvent.removeMonitor(token) }
    }
}
