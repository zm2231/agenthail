import AppKit

@MainActor
final class AncestorFrames {
    nonisolated(unsafe) private var observers: [NSObjectProtocol] = []

    func watch(_ view: NSView, moved: @escaping @MainActor () -> Void) {
        stop()
        guard let window = view.window else { return }
        observers.append(NotificationCenter.default.addObserver(forName: NSWindow.didResizeNotification, object: window, queue: nil) { _ in
            MainActor.assumeIsolated { moved() }
        })
        var ancestor = view.superview
        while let next = ancestor {
            next.postsFrameChangedNotifications = true
            observers.append(NotificationCenter.default.addObserver(forName: NSView.frameDidChangeNotification, object: next, queue: nil) { _ in
                MainActor.assumeIsolated { moved() }
            })
            ancestor = next.superview
        }
    }

    func stop() {
        observers.forEach(NotificationCenter.default.removeObserver)
        observers = []
    }

    deinit {
        observers.forEach(NotificationCenter.default.removeObserver)
    }
}
