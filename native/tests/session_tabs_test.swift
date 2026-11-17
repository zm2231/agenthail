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
        tabs.open("a")
        tabs.open("b")
        expect(ids(tabs) == ["a", "b"] && tabs.active == "b", "opening a session adds a tab and never replaces one: \(ids(tabs))")
        tabs.open("a")
        expect(ids(tabs) == ["a", "b"] && tabs.active == "a", "opening an open session switches to its tab")
        tabs.open("c")
        expect(ids(tabs) == ["a", "c", "b"], "a new tab opens next to the active tab: \(ids(tabs))")

        var full = SessionTabs()
        ["a", "b", "c", "d"].forEach { full.open($0) }
        full.activate("a")
        full.activate("d")
        var closed = full.fit(capacity: 2, holds: { $0 == "b" })
        expect(closed == ["c", "a"] && ids(full) == ["b", "d"] && full.active == "d", "a full strip closes the least recently used tabs, never the active or a held one: \(closed) \(ids(full))")
        closed = full.fit(capacity: 1, holds: { _ in true })
        expect(closed.isEmpty && ids(full) == ["b", "d"], "held tabs stay even past capacity")
        expect(full.fit(capacity: 5, holds: { _ in false }).isEmpty, "a strip with room closes nothing")

        tabs.activate("c")
        tabs.close("c")
        expect(tabs.active == "b" && ids(tabs) == ["a", "b"], "closing the active tab activates its right neighbour")
        tabs.close("b")
        expect(tabs.active == "a", "closing the last tab on the right activates its left neighbour")
        tabs.close("a")
        expect(tabs.active == nil && tabs.tabs.isEmpty, "closing every tab leaves nothing active")

        var cycle = SessionTabs()
        ["a", "b", "c"].forEach { cycle.open($0) }
        cycle.activate("c")
        expect(cycle.neighbour(1) == "a" && cycle.neighbour(-1) == "b", "switching wraps around")
        cycle.closeOthers("b")
        expect(ids(cycle) == ["b"] && cycle.active == "b", "close others keeps one tab")

        var moved = SessionTabs()
        ["a", "b", "c", "d"].forEach { moved.open($0) }
        moved.activate("b")
        let dropped = moved.reconcile(resolve: { $0 == "a" ? "a2" : $0 }, keepsTab: { $0 != "b" })
        expect(Set(ids(moved)) == ["a2", "c", "d"] && dropped == ["b"], "reconcile follows rekeys and drops gone sessions: \(ids(moved)) \(dropped)")

        var started = SessionTabs()
        started.open("a")
        started.open(SessionTabs.newSession)
        started.open("b")
        started.replace(SessionTabs.newSession, with: "c")
        expect(ids(started) == ["a", "c", "b"] && started.active == "c", "a started session replaces the New Session tab in place: \(ids(started))")
        started.replace(SessionTabs.newSession, with: "a")
        expect(ids(started) == ["a", "c", "b"] && started.active == "a", "replacing a missing tab opens the session once")

        var merged = SessionTabs()
        ["a", "b"].forEach { merged.open($0) }
        merged.reconcile(resolve: { $0 == "b" ? "a" : $0 }, keepsTab: { _ in true })
        expect(ids(merged) == ["a"] && merged.active == "a", "a rekey onto an open session merges the tabs")

        let data = try! JSONEncoder().encode(moved)
        expect((try? JSONDecoder().decode(SessionTabs.self, from: data)) == moved, "tabs round-trip for restoring at launch")
        let older = #"{"tabs":[{"id":"a","kept":false},{"id":"b","kept":true}],"active":"b"}"#
        expect((try? JSONDecoder().decode(SessionTabs.self, from: Data(older.utf8))).map(ids) == ["a", "b"], "tabs saved by an older build still restore")

        var named = SessionTabs()
        named.open("n")
        named.name("n", alias: "helper", name: "Fix the login page")
        named.reconcile(resolve: { $0 }, keepsTab: { _ in true })
        let restored = try! JSONDecoder().decode(SessionTabs.self, from: JSONEncoder().encode(named))
        let tab = restored.tabs[0]
        expect(SessionTabs.title(alias: tab.alias, name: tab.name, id: tab.id, label: "alias") == "@helper", "a restored tab shows its alias before the session list arrives")
        expect(SessionTabs.title(alias: tab.alias, name: tab.name, id: tab.id, label: "title") == "Fix the login page", "a restored tab shows its title before the session list arrives")
        named.name("n", alias: nil, name: nil)
        expect(named.tabs[0].alias == "helper", "an unknown session never erases a tab's name")
        expect(SessionTabs.title(alias: nil, name: nil, id: "x", label: "alias") == "Untitled conversation", "a tab with nothing known still has a title")

        if failures > 0 { exit(1) }
        print("session tabs: ok")
    }
}
