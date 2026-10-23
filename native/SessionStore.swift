import Foundation

struct WarmSession {
    var detail: SessionDetail
    var older: [TimelineItem] = []
    var olderCursor: Int64?
    /// The last journal row `detail` reflects; nil when only a page read can
    /// bring it current.
    var cursor: UInt64?
    /// The inspector lists every Claude subagent, not the latest page.
    var allClaudeSubagents = false

    var bytes: Int {
        (detail.timeline?.items ?? []).reduce(0) { $0 + Self.bytes($1) } + older.reduce(0) { $0 + Self.bytes($1) }
    }

    private static func bytes(_ item: TimelineItem) -> Int {
        item.text.utf8.count + item.title.utf8.count + 256
    }

    mutating func dropOlder() {
        guard !older.isEmpty else { return }
        older = []
        olderCursor = detail.timeline?.nextBefore
    }
}

@MainActor
final class SessionStore: ObservableObject {
    struct Limits {
        var sessions: Int
        var bytes: Int
        var sessionBytes: Int

        static let desktop = Limits(sessions: 16, bytes: 96 << 20, sessionBytes: 16 << 20)
        static let phone = Limits(sessions: 6, bytes: 24 << 20, sessionBytes: 8 << 20)
    }

    static let workingPin: TimeInterval = 600

    let limits: Limits
    var pinned: (String) -> Bool = { _ in false }
    var working: (String) -> Bool = { _ in false }
    var now: () -> Date = Date.init

    private var sessions: [String: WarmSession] = [:]
    private var order: [String] = []
    private var opened: [String: Date] = [:]
    /// The runtime commands each session can run, read once and then kept
    /// current by its stream.
    @Published private(set) var commands: [String: RuntimeCommands] = [:]
    private var commandReads: [String: UInt64] = [:]
    private var commandRevision: UInt64 = 0

    init(limits: Limits) {
        self.limits = limits
    }

    var ids: [String] { order }

    func open(_ id: String) -> WarmSession? {
        guard let session = sessions[id] else { return nil }
        touch(id)
        opened[id] = now()
        return session
    }

    func peek(_ id: String) -> WarmSession? {
        sessions[id]
    }

    func keep(_ session: WarmSession, for id: String) {
        sessions[id] = session
        touch(id)
        evict()
    }

    func update(_ id: String, _ change: (inout WarmSession) -> Void) {
        guard var session = sessions[id] else { return }
        change(&session)
        sessions[id] = session
    }

    func forget(_ id: String) {
        sessions.removeValue(forKey: id)
        opened.removeValue(forKey: id)
        order.removeAll { $0 == id }
    }

    func prune(keeping known: Set<String>) {
        prune { !known.contains($0) }
    }

    func prune(removing removed: (String) -> Bool) {
        for id in order where removed(id) { forget(id) }
        let gone = commands.keys.filter(removed)
        if !gone.isEmpty { gone.forEach { commands.removeValue(forKey: $0) } }
        commandReads = commandReads.filter { !removed($0.key) }
    }

    /// Starts the one read of a session's commands; nil when it was read.
    func beginCommandsRead(_ id: String) -> UInt64? {
        guard commandReads[id] == nil else { return nil }
        commandRevision &+= 1
        commandReads[id] = commandRevision
        return commandRevision
    }

    /// A read's answer, unless a streamed list replaced it while it was in
    /// flight. A failed read leaves the session without runtime commands; one
    /// that may succeed later is read again on the next open.
    func commandsRead(_ value: RuntimeCommands?, for id: String, read: UInt64, retry: Bool = false) {
        guard commandReads[id] == read else { return }
        guard let value else {
            if retry { commandReads.removeValue(forKey: id) }
            return
        }
        if commands[id] != value { commands[id] = value }
    }

    /// Lets a session's commands be read again from the route that now
    /// serves it; the current list stays until that read answers.
    func rereadCommands(_ id: String) {
        commandReads.removeValue(forKey: id)
    }

    func commandsStreamed(_ value: RuntimeCommands, for id: String) {
        commandRevision &+= 1
        commandReads[id] = commandRevision
        if commands[id] != value { commands[id] = value }
    }

    /// A carried session keeps its rows but not its cursor, which counts rows
    /// in the retired id's journal.
    func carry(_ rekeys: SessionRekeys) {
        var held = commands
        if rekeys.carry(&held) { commands = held }
        _ = rekeys.carry(&commandReads)
        guard rekeys.carry(&sessions) else { return }
        _ = rekeys.carry(&opened)
        for (id, session) in sessions where session.detail.session.id != id {
            sessions[id]?.detail.session.id = id
            sessions[id]?.cursor = nil
        }
        var seen = Set<String>()
        order = order.map(rekeys.resolve).filter { sessions[$0] != nil && seen.insert($0).inserted }
    }

    private func touch(_ id: String) {
        order.removeAll { $0 == id }
        order.append(id)
    }

    private func isPinned(_ id: String) -> Bool {
        if pinned(id) { return true }
        guard let opened = opened[id], working(id) else { return false }
        return now().timeIntervalSince(opened) < Self.workingPin
    }

    private func evict() {
        for id in order where !isPinned(id) && (sessions[id]?.bytes ?? 0) > limits.sessionBytes {
            sessions[id]?.dropOlder()
        }
        var total = sessions.values.reduce(0) { $0 + $1.bytes }
        while order.count > limits.sessions || total > limits.bytes, let victim = order.first(where: { !isPinned($0) }) {
            total -= sessions[victim]?.bytes ?? 0
            forget(victim)
        }
    }

    /// Puts a streamed row where a session page reads it: an updated row keeps
    /// its place, as its journal entry does, wherever it is shown, and a new
    /// row goes at the end.
    static func place(_ item: TimelineItem, in timeline: inout SessionTimeline, older: inout [TimelineItem]) {
        if let index = timeline.items.firstIndex(where: { $0.id == item.id }) {
            timeline.items[index] = item
        } else if let index = older.firstIndex(where: { $0.id == item.id }) {
            older[index] = item
        } else {
            timeline.items.append(item)
        }
    }

    /// A cached detail with the session's live state from the catalog, which
    /// may have moved on while the session was not shown.
    static func live(_ detail: SessionDetail, _ session: SessionState?) -> SessionDetail {
        guard let session else { return detail }
        var detail = detail
        detail.session.status = session.status
        detail.session.waitingOn = session.waitingOn
        detail.session.backgroundTasks = session.backgroundTasks
        detail.session.usageLimit = session.usageLimit
        detail.capabilities = session.capabilities
        if let readOnly = session.readOnly {
            detail.readOnly = readOnly
            detail.readOnlyReason = session.readOnlyReason ?? ""
        }
        return detail
    }
}
