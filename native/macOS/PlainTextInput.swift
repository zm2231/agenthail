import AppKit

@MainActor
final class PlainTextInput: NSObject {
    private static let shared = PlainTextInput()

    // Text views behind SwiftUI fields read these when they are made, and the
    // app's domain outranks the person's system-wide keyboard settings. Text
    // replacement ignores its default, so each text view turns it off when it
    // starts editing.
    static let substitutionDefaults = [
        "NSAutomaticDashSubstitutionEnabled",
        "NSAutomaticQuoteSubstitutionEnabled",
        "NSAutomaticSpellingCorrectionEnabled",
        "NSAutomaticPeriodSubstitutionEnabled",
        "NSAutomaticCapitalizationEnabled",
    ]

    static func install() {
        for key in substitutionDefaults { UserDefaults.standard.set(false, forKey: key) }
        let center = NotificationCenter.default
        center.removeObserver(shared)
        center.addObserver(shared, selector: #selector(beganEditing(_:)), name: NSText.didBeginEditingNotification, object: nil)
    }

    static func configure(_ view: NSTextView) {
        view.isAutomaticDashSubstitutionEnabled = false
        view.isAutomaticQuoteSubstitutionEnabled = false
        view.isAutomaticTextReplacementEnabled = false
        view.isAutomaticSpellingCorrectionEnabled = false
    }

    @objc private func beganEditing(_ note: Notification) {
        guard let view = note.object as? NSTextView else { return }
        Self.configure(view)
    }
}
