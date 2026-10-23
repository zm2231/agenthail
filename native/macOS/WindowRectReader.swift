import AppKit
import SwiftUI

/// Reports a view's rect in its window, top left origin, in points. It
/// works across split view columns, which SwiftUI coordinate spaces don't.
struct WindowRectReader: NSViewRepresentable {
    var probe: ((RectProbe) -> Void)? = nil
    let onChange: (CGRect, NSWindow) -> Void

    func makeNSView(context: Context) -> RectProbe {
        let view = RectProbe()
        view.onChange = onChange
        probe?(view)
        return view
    }

    func updateNSView(_ view: RectProbe, context: Context) {
        view.onChange = onChange
        probe?(view)
        view.post()
    }

    final class RectProbe: NSView {
        var onChange: ((CGRect, NSWindow) -> Void)?
        nonisolated(unsafe) private var observers: [NSObjectProtocol] = []
        private var last: CGRect?
        private var lastAppearance: NSAppearance.Name?

        override func hitTest(_ point: NSPoint) -> NSView? { nil }

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            observers.forEach(NotificationCenter.default.removeObserver)
            observers = []
            guard let window else { return }
            for name in [NSWindow.didResizeNotification, NSWindow.didEndLiveResizeNotification, NSWindow.didChangeBackingPropertiesNotification] {
                observers.append(NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self] _ in
                    MainActor.assumeIsolated { self?.post(force: true) }
                })
            }
            observers.append(NotificationCenter.default.addObserver(forName: NSSplitView.didResizeSubviewsNotification, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.post() }
            })
            // A column sliding or an inspector opening moves an ancestor
            // without touching this view's own frame.
            var ancestor = superview
            while let view = ancestor, view !== window.contentView {
                view.postsFrameChangedNotifications = true
                observers.append(NotificationCenter.default.addObserver(forName: NSView.frameDidChangeNotification, object: view, queue: .main) { [weak self] _ in
                    MainActor.assumeIsolated { self?.post() }
                })
                ancestor = view.superview
            }
            post(force: true)
        }

        override func layout() {
            super.layout()
            post()
        }

        override func setFrameOrigin(_ newOrigin: NSPoint) {
            super.setFrameOrigin(newOrigin)
            post()
        }

        override func setFrameSize(_ newSize: NSSize) {
            super.setFrameSize(newSize)
            post()
        }

        override func viewDidChangeEffectiveAppearance() {
            super.viewDidChangeEffectiveAppearance()
            post(force: true)
        }

        /// This view's rect in its window now, top left origin.
        func measure() -> CGRect? {
            guard let window, let content = window.contentView else { return nil }
            let inWindow = convert(bounds, to: nil)
            return CGRect(x: inWindow.minX, y: content.bounds.height - inWindow.maxY, width: inWindow.width, height: inWindow.height)
        }

        func post(force: Bool = false) {
            guard let window, let rect = measure() else { return }
            let appearance = window.effectiveAppearance.name
            guard force || rect != last || appearance != lastAppearance else { return }
            last = rect
            lastAppearance = appearance
            let onChange = onChange
            DispatchQueue.main.async { onChange?(rect, window) }
        }

        deinit {
            observers.forEach(NotificationCenter.default.removeObserver)
        }
    }
}

struct WeakProbe {
    weak var probe: WindowRectReader.RectProbe?
}
