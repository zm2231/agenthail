/// SwiftUI can update the text view between a keystroke and the binding
/// catching up, and the bound text it sees then is a keystroke old. The bound
/// text goes into the view only when it differs from the text both last
/// agreed on, so only a change from outside replaces what was typed.
@MainActor
final class ComposerTextSync {
    private(set) var agreed: String
    private var writingBack = false

    init(_ text: String) { agreed = text }

    func viewChanged(to text: String, write: (String) -> Void) {
        agreed = text
        writingBack = true
        defer { writingBack = false }
        write(text)
    }

    func textToShow(bound: String) -> String? {
        guard !writingBack, bound != agreed else { return nil }
        agreed = bound
        return bound
    }
}
