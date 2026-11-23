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

    static func transcript() -> (NSWindow, NSScrollView, TranscriptClip) {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 600), styleMask: [.titled], backing: .buffered, defer: false)
        window.alphaValue = 0
        let scroll = NSScrollView(frame: NSRect(x: 0, y: 0, width: 400, height: 600))
        scroll.hasVerticalScroller = true
        scroll.scrollerStyle = .legacy
        let document = Document(frame: NSRect(x: 0, y: 0, width: 400, height: 2000))
        scroll.documentView = document
        let container = NSView(frame: NSRect(x: 0, y: 0, width: 400, height: 600))
        container.addSubview(scroll)
        window.contentView = container
        let clip = TranscriptClip()
        let anchor = TranscriptClipAnchor.AnchorView(frame: .zero)
        anchor.clip = clip
        document.addSubview(anchor)
        anchor.register()
        return (window, scroll, clip)
    }

    static func readerInput() {
        let (window, scroll, clip) = transcript()
        let (other, _, otherClip) = transcript()

        func key(_ code: UInt16) -> NSEvent {
            NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0, windowNumber: window.windowNumber, context: nil,
                             characters: "", charactersIgnoringModifiers: "", isARepeat: false, keyCode: code)!
        }
        check(window.makeFirstResponder(scroll.verticalScroller), "the scroller takes focus")
        check(clip.movesAway(key(116), in: window), "Page Up with the transcript focused moves it away from the end")
        check(clip.movesAway(key(115), in: window), "Home with the transcript focused moves it away from the end")
        check(!clip.movesAway(key(121), in: window), "Page Down does not move away from the end")
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 10, height: 10))
        window.contentView?.addSubview(field)
        check(window.makeFirstResponder(field), "the field takes focus")
        check(!clip.movesAway(key(116), in: window), "Page Up typed into another field leaves the transcript alone")
        window.makeFirstResponder(nil)
        check(!clip.movesAway(key(116), in: window), "Page Up with nothing focused reaches no transcript")

        func wheel(_ delta: Int32, at point: NSPoint) -> NSEvent {
            let event = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: delta, wheel2: 0, wheel3: 0)!
            let screen = window.convertPoint(toScreen: point)
            event.location = CGPoint(x: screen.x, y: (NSScreen.screens.first?.frame.height ?? 0) - screen.y)
            return NSEvent(cgEvent: event)!
        }
        check(clip.movesAway(wheel(40, at: NSPoint(x: 200, y: 300)), in: window), "a wheel scroll up over the transcript moves it away from the end")
        check(!clip.movesAway(wheel(-40, at: NSPoint(x: 200, y: 300)), in: window), "a wheel scroll down over the transcript does not")
        check(!clip.movesAway(wheel(40, at: NSPoint(x: 200, y: 900)), in: window), "a wheel scroll outside the transcript does not")
        check(!otherClip.movesAway(wheel(40, at: NSPoint(x: 200, y: 300)), in: window), "a wheel scroll routed to one window leaves an overlapping window's transcript alone")
        check(!clip.movesAway(wheel(40, at: NSPoint(x: 200, y: 300)), in: other), "a wheel scroll routed to another window leaves this transcript alone")

        func press(_ point: NSPoint) -> NSEvent {
            NSEvent.mouseEvent(with: .leftMouseDown, location: point, modifierFlags: [], timestamp: 0, windowNumber: window.windowNumber, context: nil,
                               eventNumber: 0, clickCount: 1, pressure: 1)!
        }
        guard let scroller = scroll.verticalScroller else { return check(false, "the scroll view has a scroller") }
        let onScroller = scroller.convert(NSPoint(x: scroller.bounds.midX, y: scroller.bounds.midY), to: nil)
        check(clip.movesAway(press(onScroller), in: window), "a press on the scroller moves the transcript away from the end")
        check(!clip.movesAway(press(NSPoint(x: 100, y: 300)), in: window), "a press on the rows does not")
        check(!TranscriptClip().movesAway(key(116), in: window), "a clip with no transcript takes no input")
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
        readerInput()
        print("ok   transcript_clip_test")
    }
}
