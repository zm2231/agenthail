import Foundation

enum TurnNoticePreference {
    static let hiddenKey = "turnNotice.hidden"
    static let dismissedKey = "turnNotice.dismissedSessions"
    static let remembered = 500

    static func dismissed(_ stored: String) -> [String] { stored.split(separator: "\n").map(String.init) }

    static func shows(for sessionID: String, hidden: Bool, dismissed stored: String) -> Bool {
        !hidden && !dismissed(stored).contains(sessionID)
    }

    static func setting(shown: Bool, dismissed stored: String) -> (hidden: Bool, dismissed: String) {
        (!shown, shown ? "" : stored)
    }

    static func dismissing(_ sessionID: String, in stored: String) -> String {
        let kept = dismissed(stored).filter { $0 != sessionID }
        return (kept + [sessionID]).suffix(remembered).joined(separator: "\n")
    }
}
