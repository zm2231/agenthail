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
    private var forced: (width: CGFloat, fit: CGFloat, widest: CGFloat)?
    private var holding = 0

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        observers.forEach(NotificationCenter.default.removeObserver)
        observers = []
        before = nil
        forced = nil
        holding &+= 1
        guard let window else { return }
        let steps: [(Notification.Name, @MainActor @Sendable (FullScreenFitView) -> Void)] = [
            (NSWindow.willEnterFullScreenNotification, { $0.enter() }),
            (NSWindow.willExitFullScreenNotification, { $0.exit() }),
            (NSWindow.didExitFullScreenNotification, { $0.restore() }),
            (NSWindow.willStartLiveResizeNotification, { $0.holding &+= 1 }),
        ]
        for (name, step) in steps {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { if let self { step(self) } }
            })
        }
    }

    func enter() {
        holding &+= 1
        forced = nil
        guard let window, let width = pane?.fittedWidth else { return before = nil }
        before = (window.frame, width)
    }

    func exit() {
        guard let window, let pane, let before else { return }
        self.before = nil
        let docked = (pane.sidebarDocks, pane.inspectorDocks)
        let widest = window.frameRect(forContentRect: NSRect(origin: .zero, size: window.contentMinSize)).width
        pane.fitWindow(width: before.width)
        guard docked != (pane.sidebarDocks, pane.inspectorDocks) else { return }
        forced = (before.frame.width, before.width, widest)
    }

    func restore() {
        guard let forced else { return }
        self.forced = nil
        holding &+= 1
        pane?.fitWindow(width: forced.fit)
        hold(forced, turn: holding, checks: 10)
    }

    // A frame set before the columns' lower minimums reach the window is widened again, up to the docked minimum, by its next layout.
    private func hold(_ forced: (width: CGFloat, fit: CGFloat, widest: CGFloat), turn: Int, checks: Int) {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { [weak self] in
            guard let self, turn == holding, let window, !window.styleMask.contains(.fullScreen), !window.inLiveResize else { return }
            let width = window.frame.width
            if width > forced.width + 0.5, width <= forced.widest + 0.5 {
                window.setFrame(NSRect(origin: window.frame.origin, size: NSSize(width: forced.width, height: window.frame.height)), display: true)
            }
            if checks > 1 { hold(forced, turn: turn, checks: checks - 1) }
        }
    }

    deinit {
        observers.forEach(NotificationCenter.default.removeObserver)
    }
}
