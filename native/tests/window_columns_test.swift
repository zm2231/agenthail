@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class Measured {
    var detail: CGRect = .zero
}

struct Columns: View {
    @ObservedObject var pane: SessionPane
    let measured: Measured

    var body: some View {
        NavigationSplitView(columnVisibility: .constant(pane.sidebarDocks ? .all : .detailOnly)) {
            Color.red
                .navigationSplitViewColumnWidth(min: DesktopColumns.sidebar.min, ideal: DesktopColumns.sidebar.ideal, max: DesktopColumns.sidebar.max)
        } detail: {
            Color.gray
                .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { measured.detail = $0 }
                .frame(minWidth: pane.sidebarDocks ? nil : ColumnFit.windowMinimum)
                .navigationSplitViewColumnWidth(min: DesktopColumns.detail.min, ideal: DesktopColumns.detail.ideal)
                .inspector(isPresented: .constant(pane.inspectorDocks && pane.inspectorVisible)) {
                    Color.blue.inspectorColumnWidth(min: DesktopColumns.inspector.min, ideal: DesktopColumns.inspector.ideal, max: DesktopColumns.inspector.max)
                }
        }
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { pane.fitWindow(width: $0) }
    }
}

@main
struct WindowColumnsTest {
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

        let pane = SessionPane(model: AgenthailModel(connecting: false), restoresSelection: false)
        pane.fitWindow(width: 1400)
        check(pane.inspectorShown, "a wide window shows the inspector by default")
        pane.toggleInspector()
        check(!pane.inspectorVisible && !pane.inspectorShown, "toggling in a wide window hides the docked inspector")
        pane.toggleInspector()
        pane.fitWindow(width: 800)
        check(!pane.inspectorDocks && !pane.inspectorShown, "a narrow window collapses the inspector")
        check(pane.inspectorVisible, "collapsing keeps the person's choice to dock it")
        pane.toggleInspector()
        check(pane.inspectorShown && pane.inspectorOverlay && pane.inspectorVisible, "toggling in a narrow window shows the inspector over the conversation")
        pane.fitWindow(width: 1400)
        check(pane.inspectorDocks && !pane.inspectorOverlay && pane.inspectorShown, "widening docks the inspector again and drops the overlay")
        pane.toggleSidebar()
        check(!pane.sidebarOverlay, "a docked sidebar has no overlay to toggle")
        pane.fitWindow(width: 800)
        check(pane.sidebarDocks, "the sidebar stays docked while only the inspector has collapsed")
        pane.fitWindow(width: 600)
        check(!pane.sidebarDocks && !pane.inspectorDocks, "a window narrower than sidebar and conversation collapses the sidebar too")
        pane.toggleSidebar()
        check(pane.sidebarOverlay, "toggling a collapsed sidebar shows it over the conversation")
        pane.closeSidebarOverlay()
        check(!pane.sidebarOverlay, "the sidebar overlay closes")
        pane.toggleSidebar()
        pane.fitWindow(width: 900)
        check(pane.sidebarDocks && !pane.sidebarOverlay, "widening docks the sidebar again and drops its overlay")

        let live = SessionPane(model: AgenthailModel(connecting: false), restoresSelection: false)
        let measured = Measured()
        let hosting = NSHostingView(rootView: Columns(pane: live, measured: measured))
        hosting.sizingOptions = [.minSize]
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1400, height: 700), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.contentView = hosting
        // A person keeps dragging while the columns settle, so keep asking
        // for the width until the window reaches it or stops moving.
        func resize(_ width: CGFloat) -> CGFloat {
            let deadline = Date().addingTimeInterval(3)
            repeat {
                window.setContentSize(NSSize(width: width, height: 700))
                hosting.layoutSubtreeIfNeeded()
                RunLoop.main.run(until: Date().addingTimeInterval(0.15))
            } while window.contentLayoutRect.width != width && Date() < deadline
            return window.contentLayoutRect.width
        }
        check(resize(1400) == 1400 && live.inspectorDocks, "a 1400 pt window docks the inspector")
        let narrowing = resize(900)
        check(narrowing == 900 && !live.inspectorDocks, "narrowing to 900 pt collapses the inspector, got width \(narrowing), docked \(live.inspectorDocks)")
        let narrow = resize(700)
        check(narrow == 700, "with the inspector collapsed the window narrows to 700 pt, got \(narrow)")
        check(measured.detail.maxX <= narrow + 0.5 && measured.detail.width >= DesktopColumns.detail.min - 40, "the conversation fits the narrow window, got \(measured.detail)")
        let narrower = resize(500)
        check(narrower == 500 && !live.sidebarDocks, "with the sidebar collapsed the window narrows to 500 pt, got \(narrower), sidebar docked \(live.sidebarDocks)")
        check(measured.detail.minX >= -0.5 && measured.detail.maxX <= narrower + 0.5, "the conversation fills the 500 pt window, got \(measured.detail)")
        let floor = resize(300)
        check(floor == ColumnFit.windowMinimum, "the window stops at \(ColumnFit.windowMinimum) pt, got \(floor)")
        let wide = resize(1200)
        check(wide == 1200 && live.inspectorDocks && live.sidebarDocks, "widening to 1200 pt docks both columns again")

        if failures > 0 { exit(1) }
        print("window columns: the inspector, then the sidebar, collapse before they pin the window, and come back when there is room")
    }
}
