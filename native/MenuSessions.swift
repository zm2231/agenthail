import Foundation

struct MenuSessions: Equatable {
    struct Shown: Equatable {
        var needsYou: [String] = []
        var reasons: [String: String] = [:]
        var working: [String] = []
        var recent: [String] = []
    }

    var working: [String] = []
    var recent: [String] = []

    static func build(current: ScopePage.View?, rows: [String: SessionState]) -> MenuSessions {
        let families = (current?.families ?? []).compactMap { SessionTree.family(from: $0, rows: rows) }
        let working = families.filter { $0.isWorking && SessionDisplayKind.of($0.root) != .unavailable }.map(\.id)
        let busy = Set(working)
        return MenuSessions(working: working, recent: families.map(\.id).filter { !busy.contains($0) })
    }

    func shown(with inbox: DecisionInbox, limit: Int = 5) -> Shown {
        var needsYou: [String] = []
        var reasons: [String: String] = [:]
        for item in inbox.decisions where reasons[item.sessionId] == nil {
            needsYou.append(item.sessionId)
            reasons[item.sessionId] = item.reason
        }
        let asked = Set(needsYou)
        return Shown(needsYou: Array(needsYou.prefix(limit)), reasons: reasons,
                     working: Array(working.filter { !asked.contains($0) }.prefix(limit)),
                     recent: Array(recent.filter { !asked.contains($0) }.prefix(limit)))
    }
}
