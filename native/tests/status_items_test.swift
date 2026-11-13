@testable import AgenthailHelpers
import AppKit
import Foundation
import SwiftUI

@main
struct StatusItemsTest {
    @MainActor
    static func main() {
        let down = [HostHealth(id: "a", name: "vps", state: "unreachable"), HostHealth(id: "b", name: "box", state: "connected")]
        check(StatusItemsState.hosts(down) == "this Mac · vps (unreachable) · box", "a down host stays in the status bar, marked: \(StatusItemsState.hosts(down))")
        let model = AgenthailModel(connecting: false)
        let pane = model.openPane()
        let state = StatusItemsState(items: ["working", "needs-you", "hosts", "terminal"], short: false, working: 2, needsYou: 1, hosts: "this Mac")
        let host = NSHostingView(rootView: StatusItems(state: state, model: model, pane: pane).equatable().fixedSize())
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 200), styleMask: .borderless, backing: .buffered, defer: false)
        window.contentView = host
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
        host.layoutSubtreeIfNeeded()
        let size = host.fittingSize
        let line = NSFont.systemFont(ofSize: NSFont.systemFontSize).boundingRectForFont.height
        check(size.height > 0 && size.height < line * 1.8, "the status items sit on one line: \(size)")
        check(size.width > size.height * 4, "the status items run side by side: \(size)")
        withExtendedLifetime(window) {}
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
