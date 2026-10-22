@testable import AgenthailHelpers
import Foundation

@main
struct SessionTabsTest {
    static var failures = 0

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            failures += 1
        }
    }

    static func ids(_ tabs: SessionTabs) -> [String] { tabs.tabs.map(\.id) }

    static func main() {
        var tabs = SessionTabs()
        tabs.open("a", keep: false)
        tabs.open("b", keep: false)
        expect(ids(tabs) == ["b"] && tabs.preview == "b", "browsing replaces the preview tab: \(ids(tabs))")
        tabs.keep("b")
        tabs.open("c", keep: false)
        expect(ids(tabs) == ["b", "c"] && tabs.active == "c", "a kept tab stays when the next preview opens: \(ids(tabs))")
        tabs.open("b", keep: false)
        expect(ids(tabs) == ["b", "c"] && tabs.active == "b", "opening an open session switches to its tab")
        tabs.open("d", keep: true)
        expect(ids(tabs) == ["b", "d", "c"], "a kept tab opens next to the active tab: \(ids(tabs))")
        tabs.open("c", keep: true)
        expect(tabs.preview == nil, "keeping the preview tab leaves no preview")

        var noPreview = SessionTabs()
        noPreview.open("a", keep: false, preview: false)
        noPreview.open("b", keep: false, preview: false)
        expect(ids(noPreview) == ["a", "b"], "with previews off every tab is kept")

        tabs.activate("d")
        tabs.close("d")
        expect(tabs.active == "c" && ids(tabs) == ["b", "c"], "closing the active tab activates its right neighbour")
        tabs.close("c")
        expect(tabs.active == "b", "closing the last tab on the right activates its left neighbour")
        tabs.close("b")
        expect(tabs.active == nil && tabs.tabs.isEmpty, "closing every tab leaves nothing active")

        var cycle = SessionTabs()
        ["a", "b", "c"].forEach { cycle.open($0, keep: true) }
        cycle.activate("c")
        expect(cycle.neighbour(1) == "a" && cycle.neighbour(-1) == "b", "switching wraps around")
        cycle.closeOthers("b")
        expect(ids(cycle) == ["b"] && cycle.active == "b", "close others keeps one tab")

        var moved = SessionTabs()
        ["a", "b", "c", "d"].forEach { moved.open($0, keep: true) }
        moved.activate("b")
        let dropped = moved.reconcile(resolve: { $0 == "a" ? "a2" : $0 }, keepsTab: { $0 != "b" })
        expect(ids(moved) == ["a2", "c", "d"] && dropped == ["b"], "reconcile follows rekeys and drops gone sessions: \(ids(moved)) \(dropped)")
        expect(moved.active == "c", "a dropped active tab hands over to its right neighbour: \(String(describing: moved.active))")

        var started = SessionTabs()
        started.open("a", keep: true)
        started.open(SessionTabs.newSession, keep: true)
        started.open("b", keep: true)
        started.replace(SessionTabs.newSession, with: "c")
        expect(ids(started) == ["a", "c", "b"] && started.active == "c" && started.tabs[1].kept, "a started session replaces the New Session tab in place: \(ids(started))")
        started.replace(SessionTabs.newSession, with: "a")
        expect(ids(started) == ["a", "c", "b"] && started.active == "a", "replacing a missing tab opens the session once")

        var merged = SessionTabs()
        ["a", "b"].forEach { merged.open($0, keep: true) }
        merged.reconcile(resolve: { $0 == "b" ? "a" : $0 }, keepsTab: { _ in true })
        expect(ids(merged) == ["a"] && merged.active == "a", "a rekey onto an open session merges the tabs")

        let data = try! JSONEncoder().encode(moved)
        expect((try? JSONDecoder().decode(SessionTabs.self, from: data)) == moved, "tabs round-trip for restoring at launch")

        if failures > 0 { exit(1) }
        print("session tabs: ok")
    }
}
