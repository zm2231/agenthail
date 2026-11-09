import Foundation

struct MenuSessions: Equatable {
    var needsYou: [String] = []
    var working: [String] = []
    var recent: [String] = []

    static func build(needsYou: [SessionState], current: ScopePage.View?, rows: [String: SessionState], attention: [String: SessionAttention], limit: Int = 5) -> MenuSessions {
        let others = (current?.families ?? []).compactMap { SessionTree.family(from: $0, rows: rows) }.filter { attention[$0.id] == nil }
        let working = others.filter { $0.isWorking && SessionDisplayKind.of($0.root) != .unavailable }.map(\.id)
        let busy = Set(working)
        return MenuSessions(
            needsYou: needsYou.prefix(limit).map(\.id),
            working: Array(working.prefix(limit)),
            recent: others.map(\.id).filter { !busy.contains($0) }.prefix(limit).map { $0 }
        )
    }
}
