import Foundation

enum RelayFacts {
    static func state(_ relay: RelayState) -> String {
        var parts = [relay.handoff == "goal_completed" ? "Goal completed" : "Turn finished", relay.once ? "Once" : "Every time", "\(relay.pendingCount) pending", relay.active ? "active" : "complete", relay.fireCount == 1 ? "fired once" : "fired \(relay.fireCount) times"]
        if let last = relay.lastFiredAt, case let age = relativeAge(last), !age.isEmpty {
            parts.append(age == "now" ? "last just now" : "last \(age) ago")
        }
        return parts.joined(separator: " · ")
    }
}
