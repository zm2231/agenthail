import Foundation

enum SessionLaunchOutcome: Equatable {
    case opened
    case submitted(String)
    case halted(String)
    case failed(String)
}

enum SessionLaunchDecision: Equatable {
    case open(String)
    case submitted(String)
    case unconfirmed(String?)
    case halted(String, sessionID: String?)
    case failed(String)

    init(_ receipt: SessionCreationReceipt, launcher: String?, agent: String) {
        if receipt.unknown == true {
            self = .unconfirmed(receipt.id)
            return
        }
        guard receipt.ok || receipt.accepted == true else {
            if receipt.retryable == false {
                self = .halted(receipt.error ?? "The session didn't start.", sessionID: receipt.id)
            } else {
                self = .failed(receipt.error ?? "The session didn't start.")
            }
            return
        }
        if let id = receipt.id, !id.isEmpty {
            self = .open(id)
            return
        }
        let target = receipt.launcher ?? launcher ?? agent
        let note = "Submitted to \(target). It appears in the sidebar once it starts."
        self = .submitted([note, receipt.warning].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: "\n"))
    }

    var settlesRetry: Bool {
        switch self {
        case .open, .submitted: return true
        case .unconfirmed, .halted, .failed: return false
        }
    }
}
