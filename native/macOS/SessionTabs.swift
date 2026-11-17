import Foundation

/// The sessions open as tabs in one window. A tab is a view of a session;
/// opening or closing one never starts or stops anything.
struct SessionTabs: Codable, Equatable {
    struct Tab: Codable, Equatable, Identifiable {
        let id: String
        var kept: Bool
        var alias: String?
        var name: String?
    }

    /// The tab holding the New Session view; at most one is open.
    static let newSession = "agenthail.new-session"

    static func title(alias rawAlias: String?, name rawName: String?, id: String, label: String) -> String {
        if id == SessionTabs.newSession { return "New Session" }
        let alias = rawAlias.flatMap { $0.isEmpty ? nil : "@\($0)" }
        let trimmed = (rawName ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let name = trimmed.isEmpty || trimmed == id || UUID(uuidString: trimmed) != nil ? nil : SessionNames.display(trimmed)
        let parts: [String?]
        switch label {
        case "alias": parts = [alias ?? name]
        case "title": parts = [name ?? alias]
        default: parts = [alias, name]
        }
        let title = parts.compactMap { $0 }.joined(separator: " ")
        return title.isEmpty ? "Untitled conversation" : title
    }

    private(set) var tabs: [Tab] = []
    private(set) var active: String?
    private var used: [String]?

    mutating func open(_ id: String) {
        if !tabs.contains(where: { $0.id == id }) {
            let index = active.flatMap { id in tabs.firstIndex { $0.id == id } }.map { $0 + 1 } ?? tabs.count
            tabs.insert(Tab(id: id, kept: true), at: index)
        }
        activate(id)
    }

    @discardableResult
    mutating func fit(capacity: Int, holds: (String) -> Bool) -> [String] {
        var closed: [String] = []
        while tabs.count > max(capacity, 1) {
            let order = (used ?? []).filter { id in tabs.contains { $0.id == id } }
            let unused = tabs.map(\.id).filter { !order.contains($0) }
            guard let oldest = (unused + order).first(where: { $0 != active && !holds($0) }) else { break }
            close(oldest)
            closed.append(oldest)
        }
        return closed
    }

    /// Turns one tab into another in place, kept and active: the New Session
    /// tab becomes the session it started.
    mutating func replace(_ old: String, with new: String) {
        guard tabs.contains(where: { $0.id == old }) else {
            open(new)
            return
        }
        if old != new { tabs.removeAll { $0.id == new } }
        guard let index = tabs.firstIndex(where: { $0.id == old }) else { return }
        tabs[index] = Tab(id: new, kept: true)
        used = (used ?? []).map { $0 == old ? new : $0 }
        activate(new)
    }

    /// Opens a kept tab beside the active one without switching to it.
    mutating func add(_ id: String) {
        guard !tabs.contains(where: { $0.id == id }) else { return }
        let index = active.flatMap { id in tabs.firstIndex { $0.id == id } }.map { $0 + 1 } ?? tabs.count
        tabs.insert(Tab(id: id, kept: true), at: index)
    }

    mutating func name(_ id: String, alias: String?, name: String?) {
        guard let index = tabs.firstIndex(where: { $0.id == id }) else { return }
        if alias == nil, name == nil { return }
        tabs[index].alias = alias
        tabs[index].name = name
    }

    mutating func activate(_ id: String) {
        guard tabs.contains(where: { $0.id == id }) else { return }
        active = id
        used = (used ?? []).filter { $0 != id } + [id]
    }

    /// Closes a tab; the tab to its right, else its left, becomes active.
    mutating func close(_ id: String) {
        guard let index = tabs.firstIndex(where: { $0.id == id }) else { return }
        tabs.remove(at: index)
        used = used?.filter { $0 != id }
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
            survivors.append((index, Tab(id: id, kept: tab.kept, alias: tab.alias, name: tab.name)))
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

struct ClosedTab: Equatable {
    let id: String
    let title: String
}
