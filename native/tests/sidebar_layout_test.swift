@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class Measured {
    var header: CGRect = .zero
    var sidebar: CGRect = .zero
}

struct Window: View {
    let model: AgenthailModel
    let counts: [SessionFilter: Int]
    let inspector: Bool
    let measured: Measured

    var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                SessionSidebarHeader(model: model, counts: counts, help: { $0.rawValue }, newSessionHelp: "New session", findShortcut: nil, addProject: {})
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { measured.header = $0 }
                List { Text("Fixture session") }
            }
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { measured.sidebar = $0 }
            .navigationSplitViewColumnWidth(min: DesktopColumns.sidebar.min, ideal: DesktopColumns.sidebar.ideal, max: DesktopColumns.sidebar.max)
        } detail: {
            Color.gray
                .navigationSplitViewColumnWidth(min: DesktopColumns.detail.min, ideal: DesktopColumns.detail.ideal)
                .inspector(isPresented: .constant(inspector)) {
                    Color.blue.inspectorColumnWidth(min: DesktopColumns.inspector.min, ideal: DesktopColumns.inspector.ideal, max: DesktopColumns.inspector.max)
                }
        }
    }
}

@main
struct SidebarLayoutTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let model = AgenthailModel(connecting: false)
        let many: [SessionFilter: Int] = [.running: 128, .recent: 254, .all: 7910, .archived: 512]
        var failures = 0

        for width in [DesktopColumns.sidebar.min, 264, DesktopColumns.sidebar.ideal, DesktopColumns.sidebar.max] {
            let measured = Measured()
            let column = SessionSidebarHeader(model: model, counts: many, help: { $0.rawValue }, newSessionHelp: "New session", findShortcut: nil, addProject: {})
                .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { measured.header = $0 }
                .frame(width: width)
            let hosting = NSHostingView(rootView: column)
            hosting.frame = NSRect(x: 0, y: 0, width: width, height: 120)
            hosting.layoutSubtreeIfNeeded()
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
            if measured.header.width > width || measured.header.minX < 0 {
                print("FAIL: in a \(width) pt column the header lays out at \(measured.header)")
                failures += 1
            }
        }

        for inspector in [false, true] {
            for width in [720.0, 900, 1200, 1600] {
                let measured = Measured()
                let hosting = NSHostingView(rootView: Window(model: model, counts: many, inspector: inspector, measured: measured))
                let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: width, height: 700), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
                window.contentView = hosting
                hosting.layoutSubtreeIfNeeded()
                RunLoop.main.run(until: Date().addingTimeInterval(0.3))
                let label = "window \(Int(width)) pt, inspector \(inspector ? "open" : "closed")"
                if measured.sidebar.width > DesktopColumns.sidebar.max || measured.sidebar.minX < 0 {
                    print("FAIL: \(label): the sidebar is \(measured.sidebar), wider than its column or pushed off the left edge")
                    failures += 1
                }
                if measured.header.minX < measured.sidebar.minX || measured.header.maxX > measured.sidebar.maxX {
                    print("FAIL: \(label): the header \(measured.header) overflows the sidebar \(measured.sidebar)")
                    failures += 1
                }
            }
        }

        if failures > 0 { exit(1) }
        print("sidebar layout: the header fits its column at every window width")
    }
}
