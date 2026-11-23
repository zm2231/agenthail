@testable import AgenthailHelpers
import AppKit
import SwiftUI

struct AgentBoundFields: View {
    @State var composer = ""
    @State var answer = ""
    @State var editor = ""

    var body: some View {
        VStack {
            ComposerTextView(text: $composer, focusRequest: 0, placeholder: "Message") {}
                .frame(width: 300, height: 60)
            TextField("Answer", text: $answer, axis: .vertical)
                .lineLimit(2...6)
            TextEditor(text: $editor)
                .frame(width: 300, height: 60)
        }
    }
}

@main
@MainActor
struct PlainTextInputTest {
    static let typed = "--json FILE... \"double\" 'single' a -- b (c) -> d"
    static var failed = false

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            failed = true
            print("FAIL: \(message)")
        }
    }

    static func main() {
        _ = NSApplication.shared
        let domain = Bundle.main.bundleIdentifier ?? ProcessInfo.processInfo.processName
        defer { UserDefaults.standard.removePersistentDomain(forName: domain) }
        PlainTextInput.install()

        check(!NSSpellChecker.isAutomaticDashSubstitutionEnabled, "smart dashes are off app-wide")
        check(!NSSpellChecker.isAutomaticQuoteSubstitutionEnabled, "smart quotes are off app-wide")
        check(!NSSpellChecker.isAutomaticSpellingCorrectionEnabled, "auto-correction is off app-wide")
        check(!NSSpellChecker.isAutomaticPeriodSubstitutionEnabled, "double-space period is off app-wide")
        check(!NSSpellChecker.isAutomaticCapitalizationEnabled, "auto-capitalization is off app-wide")

        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled], backing: .buffered, defer: false)

        let smart = NSTextView()
        window.contentView = smart
        window.makeFirstResponder(smart)
        smart.insertText("x", replacementRange: smart.selectedRange())
        smart.isAutomaticDashSubstitutionEnabled = true
        smart.isAutomaticQuoteSubstitutionEnabled = true
        smart.string = ""
        smart.insertText(typed, replacementRange: smart.selectedRange())
        smart.checkTextInDocument(nil)
        check(smart.string != typed, "a text view with smart dashes and quotes on rewrites the sample, so the checks below can catch it: \(smart.string.debugDescription)")

        let composer = ComposerNSTextView.make()
        window.contentView = composer
        window.makeFirstResponder(composer)
        sendsAsTyped(composer, "the composer")
        check(Array(composer.draftText.utf8) == Array(typed.utf8), "the composer's draft is byte for byte what was typed: \(composer.draftText.debugDescription)")

        let hosting = NSHostingView(rootView: AgentBoundFields())
        hosting.frame = NSRect(x: 0, y: 0, width: 400, height: 300)
        window.contentView = hosting
        hosting.layoutSubtreeIfNeeded()
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
        hosting.layoutSubtreeIfNeeded()

        let textViews = descendants(of: hosting).compactMap { $0 as? NSTextView }
        let hostedComposer = textViews.first { $0 is ComposerNSTextView }
        let editor = textViews.first { !($0 is ComposerNSTextView) }
        check(hostedComposer != nil && editor != nil, "the hosted composer and SwiftUI editor are found: \(textViews.map { type(of: $0) })")
        if let hostedComposer {
            window.makeFirstResponder(hostedComposer)
            sendsAsTyped(hostedComposer, "the composer the session and new-session views host")
        }
        if let editor {
            window.makeFirstResponder(editor)
            sendsAsTyped(editor, "a SwiftUI text editor")
        }

        let field = descendants(of: hosting).compactMap { $0 as? NSTextField }.first { $0.isEditable }
        check(field != nil, "the SwiftUI answer field is found")
        if let field {
            window.makeFirstResponder(field)
            if let fieldEditor = field.currentEditor() as? NSTextView {
                sendsAsTyped(fieldEditor, "a SwiftUI answer field")
            } else {
                check(false, "the SwiftUI answer field edits through a text view")
            }
        }

        if failed { exit(1) }
        print("plain text input: ok")
    }

    static func sendsAsTyped(_ view: NSTextView, _ name: String) {
        view.string = ""
        view.insertText(typed, replacementRange: view.selectedRange())
        check(!view.isAutomaticDashSubstitutionEnabled, "\(name) has smart dashes off")
        check(!view.isAutomaticQuoteSubstitutionEnabled, "\(name) has smart quotes off")
        check(!view.isAutomaticTextReplacementEnabled, "\(name) has text replacement off once it is edited")
        check(!view.isAutomaticSpellingCorrectionEnabled, "\(name) has auto-correction off")
        view.checkTextInDocument(nil)
        check(Array(view.string.utf8) == Array(typed.utf8), "\(name) keeps the typed text byte for byte: \(view.string.debugDescription)")
    }

    static func descendants(of view: NSView) -> [NSView] {
        view.subviews.flatMap { [$0] + descendants(of: $0) }
    }
}
