import Foundation

struct FinishedSessions: Equatable {
    private(set) var unseen: Set<String> = []

    func contains(_ id: String) -> Bool { unseen.contains(id) }

    mutating func track(previous: [SessionState], current: [SessionState], viewing: (String) -> Bool) {
        let wasWorking = Set(previous.filter(Self.busy).map(\.id))
        var next = unseen
        for session in current {
            if Self.busy(session) {
                next.remove(session.id)
            } else if wasWorking.contains(session.id), !viewing(session.id) {
                next.insert(session.id)
            }
        }
        next.formIntersection(current.map(\.id))
        if next != unseen { unseen = next }
    }

    mutating func markSeen(_ id: String) {
        if unseen.contains(id) { unseen.remove(id) }
    }

    private static func busy(_ session: SessionState) -> Bool { session.isWorking || session.isWaitingOnBackground }
}
