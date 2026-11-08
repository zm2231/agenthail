import Foundation

enum PairingProgress: Equatable {
    case waiting
    case paired(String)
    case expired

    static let expiredNotice = "The pairing code expired. Pair again to show a new one."
    static let interval: Duration = .milliseconds(1500)

    static func of(_ status: PairingStatusResponse?, pairing: PairingResponse, now: Date) -> PairingProgress {
        switch status?.state {
        case "paired": return .paired(status?.device?.name ?? "the iPhone")
        case "expired": return .expired
        case "pending": return .waiting
        default: return expired(pairing, now: now) ? .expired : .waiting
        }
    }

    static func expired(_ pairing: PairingResponse, now: Date) -> Bool {
        guard let deadline = TurnClock.date(pairing.expiresAt) else { return false }
        return now >= deadline
    }

    static func deadline(_ pairing: PairingResponse) -> String {
        guard let time = TurnClock.date(pairing.expiresAt)?.formatted(date: .omitted, time: .shortened) else {
            return "The code works once, for five minutes."
        }
        return "The code works once, before \(time)."
    }

    var notice: String? {
        switch self {
        case .waiting: return nil
        case let .paired(name): return "Paired \(name)."
        case .expired: return Self.expiredNotice
        }
    }
}
