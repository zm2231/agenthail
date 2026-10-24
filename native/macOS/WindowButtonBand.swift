import AppKit
import SwiftUI

extension View {
    func windowButtonBand(leading: CGFloat) -> some View {
        background(WindowButtonBand(leading: leading).allowsHitTesting(false))
    }
}

private struct WindowButtonBand: NSViewRepresentable {
    let leading: CGFloat

    func makeNSView(context: Context) -> WindowButtonBandView { WindowButtonBandView() }

    func updateNSView(_ view: WindowButtonBandView, context: Context) {
        view.leading = leading
        view.apply()
    }
}

final class WindowButtonBandView: NSView {
    var leading: CGFloat = WindowButtons.paneInset
    nonisolated(unsafe) private var observers: [NSObjectProtocol] = []
    private let frames = AncestorFrames()

    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        observers.forEach(NotificationCenter.default.removeObserver)
        observers = []
        guard let window else { return frames.stop() }
        frames.watch(self) { [weak self] in self?.apply() }
        let resets: [Notification.Name] = [
            NSWindow.didResizeNotification, NSWindow.didEndLiveResizeNotification, NSWindow.didExitFullScreenNotification,
            NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification, NSWindow.didBecomeMainNotification, NSWindow.didResignMainNotification,
        ]
        for name in resets {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.apply() }
            })
        }
        // AppKit moves the buttons back on its own titlebar layout passes, which post nothing else.
        for button in WindowButtons.kinds.compactMap(window.standardWindowButton) {
            button.postsFrameChangedNotifications = true
            observers.append(NotificationCenter.default.addObserver(forName: NSView.frameDidChangeNotification, object: button, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.queue() }
            })
        }
        apply()
    }

    override func layout() {
        super.layout()
        apply()
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        apply()
    }

    func apply() {
        place()
        queue()
    }

    private func queue() {
        guard !queued else { return }
        queued = true
        DispatchQueue.main.async { [weak self] in
            self?.queued = false
            self?.place()
        }
    }

    private var queued = false

    private func place() {
        guard let window, !window.styleMask.contains(.fullScreen) else { return }
        WindowButtons.place(in: window, band: convert(bounds, to: nil), leading: leading)
    }

    deinit {
        observers.forEach(NotificationCenter.default.removeObserver)
    }
}
