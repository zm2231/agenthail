import Foundation

@MainActor
final class ScopeTreeCache {
    private struct Entry {
        let view: ScopePage.View?
        let rows: [String: SessionState]
        let attention: [String: SessionAttention]
        let tree: SessionTree
    }

    private var entries: [ScopeRequest: Entry] = [:]
    private(set) var builds = 0

    func tree(for request: ScopeRequest, view: ScopePage.View?, rows: [String: SessionState], attention: [String: SessionAttention], cells: [String: SessionCell], keeping requests: Set<ScopeRequest>) -> SessionTree {
        entries = entries.filter { requests.contains($0.key) }
        if let entry = entries[request], entry.view == view, entry.rows == rows, entry.attention == attention { return entry.tree }
        builds += 1
        let tree = SessionTree.ordered(list: view, current: nil, rows: rows, attention: attention, cells: cells)
        entries[request] = Entry(view: view, rows: rows, attention: attention, tree: tree)
        return tree
    }
}
