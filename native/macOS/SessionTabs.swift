import Foundation

/// The sessions open as tabs in one window. A tab is a view of a session;
/// opening or closing one never starts or stops anything.
struct SessionTabs: Codable, Equatable {
    struct Tab: Codable, Equatable, Identifiable {
        let id: String
        var kept: Bool
    }

    /// The tab holding the New Session view; at most one is open.
    static let newSession = "agenthail.new-session"

    private(set) var tabs: [Tab] = []
    private(set) var active: String?

    var preview: String? { tabs.first { !$0.kept }?.id }

    /// Shows a session: switches to its tab, or opens one. Unless kept, the
    /// new tab replaces the preview tab, so browsing doesn't pile up tabs.
    mutating func open(_ id: String, keep: Bool, preview previews: Bool = true) {
        if let index = tabs.firstIndex(where: { $0.id == id }) {
            if keep { tabs[index].kept = true }
            active = id
            return
        }
        let tab = Tab(id: id, kept: keep || !previews)
        if !tab.kept, let index = tabs.firstIndex(where: { !$0.kept }) {
            tabs[index] = tab
        } else if let active, let index = tabs.firstIndex(where: { $0.id == active }) {
            tabs.insert(tab, at: index + 1)
        } else {
            tabs.append(tab)
        }
        active = id
    }

    /// Turns one tab into another in place, kept and active: the New Session
    /// tab becomes the session it started.
    mutating func replace(_ old: String, with new: String) {
        guard tabs.contains(where: { $0.id == old }) else {
            open(new, keep: true)
            return
        }
        if old != new { tabs.removeAll { $0.id == new } }
        guard let index = tabs.firstIndex(where: { $0.id == old }) else { return }
        tabs[index] = Tab(id: new, kept: true)
        active = new
    }

    mutating func keep(_ id: String) {
        guard let index = tabs.firstIndex(where: { $0.id == id }) else { return }
        tabs[index].kept = true
    }

    mutating func activate(_ id: String) {
        if tabs.contains(where: { $0.id == id }) { active = id }
    }

    /// Closes a tab; the tab to its right, else its left, becomes active.
    mutating func close(_ id: String) {
        guard let index = tabs.firstIndex(where: { $0.id == id }) else { return }
        tabs.remove(at: index)
        guard active == id else { return }
        active = tabs.isEmpty ? nil : tabs[min(index, tabs.count - 1)].id
    }

    mutating func closeOthers(_ id: String) {
        guard tabs.contains(where: { $0.id == id }) else { return }
        tabs.removeAll { $0.id != id }
        active = id
    }

    func neighbour(_ offset: Int) -> String? {
        guard !tabs.isEmpty else { return nil }
        let index = active.flatMap { id in tabs.firstIndex { $0.id == id } } ?? 0
        let count = tabs.count
        return tabs[((index + offset) % count + count) % count].id
    }

    /// Follows sessions that changed id and drops tabs whose session is
    /// gone, choosing the next active tab as close does. Returns the ids
    /// dropped.
    @discardableResult
    mutating func reconcile(resolve: (String) -> String, keepsTab: (String) -> Bool) -> [String] {
        var dropped: [String] = []
        var survivors: [(index: Int, tab: Tab)] = []
        let activeIndex = active.flatMap { id in tabs.firstIndex { $0.id == id } }
        for (index, tab) in tabs.enumerated() {
            let id = resolve(tab.id)
            guard keepsTab(id), !survivors.contains(where: { $0.tab.id == id }) else {
                dropped.append(tab.id)
                continue
            }
            survivors.append((index, Tab(id: id, kept: tab.kept)))
        }
        if let activeIndex, let current = active {
            let resolved = resolve(current)
            let kept = survivors.first { $0.tab.id == resolved }
                ?? survivors.first { $0.index > activeIndex }
                ?? survivors.last
            active = kept?.tab.id
        }
        tabs = survivors.map(\.tab)
        return dropped
    }
}
