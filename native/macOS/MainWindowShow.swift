import AppKit

enum MainWindowShow {
    enum Step: Equatable {
        case bringForward
        case open
    }

    static func step(visible: Bool, miniaturized: Bool) -> Step {
        visible || miniaturized ? .bringForward : .open
    }

    @MainActor
    static func show(_ window: NSWindow?, open: () -> Void) {
        NSApplication.shared.activate()
        guard let window, step(visible: window.isVisible, miniaturized: window.isMiniaturized) == .bringForward else {
            open()
            return
        }
        if window.isMiniaturized { window.deminiaturize(nil) }
        window.makeKeyAndOrderFront(nil)
    }
}
