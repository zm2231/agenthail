import AppKit

enum PlainTextInput {
    // Text views behind SwiftUI fields read these when they are made, and the
    // app's domain outranks the person's system-wide keyboard settings.
    static let substitutionDefaults = [
        "NSAutomaticDashSubstitutionEnabled",
        "NSAutomaticQuoteSubstitutionEnabled",
        "NSAutomaticTextReplacementEnabled",
        "NSAutomaticSpellingCorrectionEnabled",
        "NSAutomaticPeriodSubstitutionEnabled",
        "NSAutomaticCapitalizationEnabled",
    ]

    static func install() {
        for key in substitutionDefaults { UserDefaults.standard.set(false, forKey: key) }
    }

    @MainActor
    static func configure(_ view: NSTextView) {
        view.isAutomaticDashSubstitutionEnabled = false
        view.isAutomaticQuoteSubstitutionEnabled = false
        view.isAutomaticTextReplacementEnabled = false
        view.isAutomaticSpellingCorrectionEnabled = false
    }
}
