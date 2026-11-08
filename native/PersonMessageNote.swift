import Foundation

enum PersonMessageNote {
    static let deliveredAsNote = "Delivered as a note"
    static let sendsAsNote = "Sends as a note"

    static func statusLine(_ capabilities: Capabilities) -> String {
        capabilities.reachesAsNote ? sendsAsNote : ""
    }

    static func text(_ capabilities: Capabilities, canAdopt: Bool) -> String {
        guard capabilities.reachesAsNote else { return "" }
        var text = "What you write reaches this terminal session as a note from Agenthail, not as your own turn."
        if capabilities.remoteControl { text += " Run /remote-control in its terminal to write as yourself." }
        if canAdopt { text += " Open in Agenthail also makes it your own turn." }
        return text
    }

    static func receiptNote(_ deliveredAs: String?) -> String? {
        deliveredAs == "note" ? deliveredAsNote : nil
    }

    static func receiptLine(_ base: String, receipt: DeliveryReceipt?) -> String {
        let notice = receipt?.notice.flatMap { $0.isEmpty ? nil : $0 }
        return [base, receiptNote(receipt?.deliveredAs), notice].compactMap { $0 }.joined(separator: " · ")
    }
}
