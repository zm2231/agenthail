import Foundation

enum DaemonOrder {
    static func rank(_ views: [ScopePage.View]) -> [String: Int] {
        var order: [String: Int] = [:]
        for member in views.flatMap({ $0.families.flatMap(\.members) }) where order[member.id] == nil {
            order[member.id] = order.count
        }
        return order
    }

    static func sorted(_ sessions: [SessionState], by order: [String: Int]) -> [SessionState] {
        sessions.enumerated().sorted { lhs, rhs in
            let (left, right) = (order[lhs.element.id] ?? Int.max, order[rhs.element.id] ?? Int.max)
            return left != right ? left < right : lhs.offset < rhs.offset
        }.map(\.element)
    }
}
