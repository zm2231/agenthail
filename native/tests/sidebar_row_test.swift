@testable import AgenthailHelpers
import AppKit
import Combine
import Foundation
import SwiftUI

@MainActor
final class RenderCounts {
    var counts: [String: Int] = [:]
}

@MainActor
final class Selection: ObservableObject {
    @Published var selected = "a"
    @Published var unrelated = 0
}

struct ProbeRow: View, @MainActor Equatable {
    let state: SidebarRowState
    let renders: RenderCounts

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.state == rhs.state }

    var body: some View {
        renders.counts[state.session.id, default: 0] += 1
        return Text(state.title).fontWeight(state.selected ? .bold : .regular)
    }
}

struct ProbeList: View {
    @ObservedObject var selection: Selection
    let sessions: [SessionState]
    let renders: RenderCounts

    var body: some View {
        let _ = selection.unrelated
        VStack {
            ForEach(sessions) { session in
                ProbeRow(state: SidebarRowState(session: session, title: session.name, selected: selection.selected == session.id), renders: renders)
                    .equatable()
            }
        }
    }
}

@main
struct SidebarRowTest {
    static func session(_ id: String, name: String? = nil, archived: Bool? = nil) -> SessionState {
        var row = SessionState(id: id, surface: "claude", name: name ?? id, alias: nil, status: "idle", lastActive: "2026-11-06T08:00:00Z", queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(respond: true), readOnly: false, readOnlyReason: "")
        row.archived = archived
        return row
    }

    static func listing(_ sessions: [SessionState]) -> DashboardSnapshot {
        DashboardSnapshot(updatedAt: "2026-11-06T08:00:00Z", eventCursor: 1, hostEpoch: "h", catalogSeq: 1, daemon: DaemonState(running: true, pid: 1, stale: false, refreshError: nil), surfaces: [], sessions: sessions, totalSessions: sessions.count, queue: [], channels: [], relays: [], history: [], attention: [], deliveryProblems: nil, codexRecentHours: 5, busyDelivery: "queue")
    }

    @MainActor
    static func main() {
        let base = SidebarRowState(session: session("a"), title: "a")
        check(base == SidebarRowState(session: session("a"), title: "a"), "rows built from the same values are equal")
        let question = SessionAttention(kind: .request, reason: "Waiting on you")
        let changed: [(String, SidebarRowState)] = [
            ("selection", SidebarRowState(session: session("a"), title: "a", selected: true)),
            ("attention", SidebarRowState(session: session("a"), title: "a", attention: question)),
            ("title", SidebarRowState(session: session("a"), title: "renamed")),
            ("unseen", SidebarRowState(session: session("a"), title: "a", finishedUnseen: true)),
            ("lead", SidebarRowState(session: session("a"), title: "a", canMakeLead: true)),
            ("reason", SidebarRowState(session: session("a"), title: "a", showsReason: true)),
            ("session", SidebarRowState(session: session("a", archived: true), title: "a")),
        ]
        for (name, other) in changed { check(base != other, "a \(name) change re-renders the row") }

        let renders = RenderCounts()
        let selection = Selection()
        let sessions = (0..<10).map { session(["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"][$0]) }
        let host = NSHostingView(rootView: ProbeList(selection: selection, sessions: sessions, renders: renders))
        host.frame = NSRect(x: 0, y: 0, width: 200, height: 400)
        let window = NSWindow(contentRect: host.frame, styleMask: .borderless, backing: .buffered, defer: false)
        window.contentView = host
        func settle() {
            RunLoop.main.run(until: Date().addingTimeInterval(0.1))
            host.layoutSubtreeIfNeeded()
        }
        settle()
        check(renders.counts.count == 10, "every row renders once to start: \(renders.counts)")
        renders.counts = [:]
        selection.unrelated += 1
        settle()
        check(renders.counts.isEmpty, "a list update that changes no row redraws none: \(renders.counts)")
        selection.selected = "e"
        settle()
        check(renders.counts == ["a": 1, "e": 1], "a selection change redraws only the two rows whose selection changed: \(renders.counts)")

        let model = AgenthailModel(connecting: false)
        let pane = model.openPane()
        model.snapshot = listing([session("a", name: "first"), session("b")])
        let actions = SidebarRowActions(shown: session("a", name: "first"), model: model, pane: pane)
        model.snapshot = listing([session("a", name: "renamed since the row last drew"), session("b")])
        actions.rename()
        check(pane.renamingSession?.name == "renamed since the row last drew", "Rename acts on the session as it is now, not as the row last drew it: \(pane.renamingSession?.name ?? "nil")")
        actions.select(keep: false)
        check(pane.selectedSessionID == "a", "selecting from a row selects its session")
        withExtendedLifetime(window) {}
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
