import AppKit
import SwiftUI

extension View {
    func fitsAfterFullScreen(_ pane: SessionPane) -> some View {
        background(FullScreenFit(pane: pane).allowsHitTesting(false))
    }
}

private struct FullScreenFit: NSViewRepresentable {
    let pane: SessionPane

    func makeNSView(context: Context) -> FullScreenFitView { FullScreenFitView() }

    func updateNSView(_ view: FullScreenFitView, context: Context) {
        view.pane = pane
    }
}

final class FullScreenFitView: NSView {
    weak var pane: SessionPane?
    nonisolated(unsafe) private var observers: [NSObjectProtocol] = []
    private var before: (frame: NSRect, width: CGFloat)?

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        observers.forEach(NotificationCenter.default.removeObserver)
        observers = []
        guard let window else { return }
        let steps: [(Notification.Name, @MainActor @Sendable (FullScreenFitView) -> Void)] = [
            (NSWindow.willEnterFullScreenNotification, { $0.enter() }),
            (NSWindow.willExitFullScreenNotification, { $0.exit() }),
            (NSWindow.didExitFullScreenNotification, { $0.restore() }),
        ]
        for (name, step) in steps {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { if let self { step(self) } }
            })
        }
    }

    func enter() {
        guard let window, let width = pane?.fittedWidth else { return before = nil }
        before = (window.frame, width)
    }

    func exit() {
        guard let before else { return }
        pane?.fitWindow(width: before.width)
    }

    func restore() {
        guard let before else { return }
        self.before = nil
        pane?.fitWindow(width: before.width)
        hold(width: before.frame.width, checks: 10)
    }

    // A frame set before the columns' lower minimums reach the window is widened again by its next layout.
    private func hold(width: CGFloat, checks: Int) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { [weak self] in
            guard let self, let window, !window.styleMask.contains(.fullScreen), !window.inLiveResize else { return }
            if window.frame.width > width + 0.5 {
                window.setFrame(NSRect(origin: window.frame.origin, size: NSSize(width: width, height: window.frame.height)), display: true)
            }
            if checks > 1 { hold(width: width, checks: checks - 1) }
        }
    }

    deinit {
        observers.forEach(NotificationCenter.default.removeObserver)
    }
}
