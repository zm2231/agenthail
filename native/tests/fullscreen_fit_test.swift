@testable import AgenthailHelpers
import AppKit
import SwiftUI

struct FitColumns: View {
    var pane: SessionPane
    var fits: Bool

    var body: some View {
        let sidebar = pane.sidebarShown ? DesktopColumns.sidebar : (min: 0, ideal: 0, max: 0)
        let split = NavigationSplitView(columnVisibility: .constant(.all)) {
            Color.red.navigationSplitViewColumnWidth(min: sidebar.min, ideal: sidebar.ideal, max: sidebar.max)
        } detail: {
            Color.gray
                .conversationColumn(sidebarDocks: pane.sidebarShown)
                .navigationSplitViewColumnWidth(min: DesktopColumns.detail.min, ideal: DesktopColumns.detail.ideal)
        }
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { pane.fitWindow(width: $0) }
        if fits { split.fitsAfterFullScreen(pane) } else { split }
    }
}

@main
struct FullScreenFitTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }

        // AppKit restores the frame in one step on leaving full screen, without a person's drag to keep asking for the width.
        func leaveFullScreen(fits: Bool, animates: Bool, from start: CGFloat = 600, thenWiden widened: CGFloat? = nil) -> (width: CGFloat, docked: Bool) {
            let pane = SessionPane(model: AgenthailModel(connecting: false), restoresSelection: false)
            let hosting = NSHostingView(rootView: FitColumns(pane: pane, fits: fits))
            hosting.sizingOptions = [.minSize]
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: start, height: 700), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
            window.contentView = hosting
            func settle() { for _ in 0..<6 { hosting.layoutSubtreeIfNeeded(); RunLoop.main.run(until: Date().addingTimeInterval(0.1)) } }
            settle()
            let narrow = window.frame
            NotificationCenter.default.post(name: NSWindow.willEnterFullScreenNotification, object: window)
            window.setContentSize(NSSize(width: 1400, height: 900))
            settle()
            check(pane.sidebarDocks, "full screen docks the sidebar")
            NotificationCenter.default.post(name: NSWindow.willExitFullScreenNotification, object: window)
            if animates { settle() }
            window.setFrame(narrow, display: false)
            NotificationCenter.default.post(name: NSWindow.didExitFullScreenNotification, object: window)
            if let widened {
                RunLoop.main.run(until: Date().addingTimeInterval(0.25))
                window.setFrame(NSRect(origin: window.frame.origin, size: NSSize(width: widened, height: window.frame.height)), display: false)
            }
            settle()
            settle()
            return (window.contentLayoutRect.width, pane.sidebarDocks)
        }

        for animates in [true, false] {
            let bare = leaveFullScreen(fits: false, animates: animates)
            let fitted = leaveFullScreen(fits: true, animates: animates)
            check(bare.width > 600 + 0.5, "without the fit, leaving full screen widens a 600 pt window, got \(bare.width)")
            check(fitted.width == 600, "leaving full screen returns a 600 pt window to 600 pt, got \(fitted.width), animates \(animates)")
            check(!fitted.docked, "and the sidebar undocks there, below \(ColumnFit.sidebarDocks) pt")
            let widened = leaveFullScreen(fits: true, animates: animates, thenWiden: 900)
            check(widened.width == 900, "a window widened right after leaving full screen keeps the width it was given, got \(widened.width), animates \(animates)")
        }
        let wide = leaveFullScreen(fits: true, animates: false, from: 1100, thenWiden: 1250)
        check(wide.width == 1250 && wide.docked, "a window that entered full screen wide enough to dock is left to its own sizes, got \(wide)")

        if failures > 0 { exit(1) }
        print("full screen fit tests passed")
    }
}
