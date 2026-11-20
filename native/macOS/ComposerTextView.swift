import AppKit
import SwiftUI

struct ComposerTextView: NSViewRepresentable {
    @Binding var text: String
    @Binding var focused: Bool
    let placeholder: String
    var accessibilityLabel: String? = nil
    var lines: ClosedRange<Int> = 2...10
    var windowShare: CGFloat? = nil
    var onPaste: ((PastedAttachments) -> Void)? = nil
    var onCommand: ((ComposerCommand) -> Bool)? = nil
    var onUpFromFirstLine: (() -> Bool)? = nil
    var onDownFromLastLine: (() -> Bool)? = nil
    var editor: ComposerEditor? = nil
    var onCaret: ((Int) -> Void)? = nil
    var completionKey: ((CompletionKey) -> Bool)? = nil
    var draftID: String? = nil
    let onSubmit: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let textView = ComposerNSTextView.make()
        textView.delegate = context.coordinator
        textView.placeholder = placeholder
        textView.setAccessibilityLabel(accessibilityLabel ?? placeholder)
        textView.load(text)
        context.coordinator.sync = ComposerTextSync(text)
        context.coordinator.draftID = draftID
        textView.setSelectedRange(NSRange(location: (textView.string as NSString).length, length: 0))
        textView.command = { ComposerCommand.matching($0, in: .shared) }
        textView.onCommand = { [weak coordinator = context.coordinator] in coordinator?.parent.onCommand?($0) ?? false }
        textView.onUpFromFirstLine = { [weak coordinator = context.coordinator] in coordinator?.parent.onUpFromFirstLine?() ?? false }
        textView.onDownFromLastLine = { [weak coordinator = context.coordinator] in coordinator?.parent.onDownFromLastLine?() ?? false }
        textView.onSubmit = { [weak coordinator = context.coordinator] in coordinator?.parent.onSubmit() }
        textView.completionKey = { [weak coordinator = context.coordinator] in coordinator?.parent.completionKey?($0) ?? false }
        editor?.textView = textView
        textView.focusChanged = { [weak coordinator = context.coordinator] isFocused in
            coordinator?.focusChanged(isFocused)
        }

        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.documentView = textView
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.parent = self
        guard let textView = scroll.documentView as? ComposerNSTextView else { return }
        textView.isEditable = context.environment.isEnabled
        if textView.font?.pointSize != DesktopType.prose {
            textView.font = ComposerNSTextView.font
            textView.typingAttributes[.font] = ComposerNSTextView.font
        }
        textView.pasteAttachments = onPaste
        editor?.textView = textView
        textView.placeholder = placeholder
        textView.setAccessibilityLabel(accessibilityLabel ?? placeholder)
        textView.placeholderColor = NSColor(DesktopPalette.text2.resolve(in: context.environment))
        if context.coordinator.draftID != draftID {
            context.coordinator.draftID = draftID
            context.coordinator.sync = ComposerTextSync(text)
            textView.load(text)
        }
        textView.show(bound: text, sync: context.coordinator.sync)
        context.coordinator.focus(textView, requested: focused)
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView scroll: NSScrollView, context _: Context) -> CGSize? {
        guard let width = proposal.width, width > 0, let textView = scroll.documentView as? ComposerNSTextView else { return nil }
        return CGSize(width: width, height: textView.fittingHeight(width: width, lines: Self.lines(lines, share: windowShare, window: scroll.window?.contentLayoutRect.height)))
    }

    static func lines(_ lines: ClosedRange<Int>, share: CGFloat?, window height: CGFloat?) -> ClosedRange<Int> {
        guard let share, let height, height > 0 else { return lines }
        return lines.lowerBound...max(lines.lowerBound, Int(height * share / ComposerNSTextView.lineHeight))
    }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: ComposerTextView
        var sync: ComposerTextSync
        var draftID: String?
        private var focusTaken = false

        init(_ parent: ComposerTextView) {
            self.parent = parent
            sync = ComposerTextSync(parent.text)
        }

        func textDidChange(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            textView.needsDisplay = true
            sync.viewChanged(to: (textView as? ComposerNSTextView)?.draftText ?? textView.string) { if parent.text != $0 { parent.text = $0 } }
            caretMoved(textView)
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            caretMoved(textView)
        }

        // The text view also moves the caret while SwiftUI updates the view,
        // when it replaces the text, and state can't change during an update.
        private func caretMoved(_ textView: NSTextView) {
            let location = textView.selectedRange().location
            let caret = (textView as? ComposerNSTextView)?.draftOffset(location) ?? location
            DispatchQueue.main.async { [self] in parent.onCaret?(caret) }
        }

        // Once per request: the binding lags a resign, and an update in that gap must not take focus back.
        func focus(_ textView: NSTextView, requested: Bool) {
            guard requested else { return focusTaken = false }
            guard !focusTaken, let window = textView.window else { return }
            focusTaken = true
            if window.firstResponder !== textView {
                DispatchQueue.main.async { textView.window?.makeFirstResponder(textView) }
            }
        }

        func focusChanged(_ isFocused: Bool) {
            if isFocused { focusTaken = true }
            DispatchQueue.main.async { [self] in
                if parent.focused != isFocused { parent.focused = isFocused }
            }
        }
    }
}

/// Replaces part of the composer's text through the text view, so the edit
/// joins its undo and the caret lands after it.
@MainActor
final class ComposerEditor {
    weak var textView: NSTextView?

    func replace(_ range: NSRange, with text: String, caret: Int) {
        guard let textView else { return }
        let composer = textView as? ComposerNSTextView
        let range = composer?.viewRange(range) ?? range
        guard NSMaxRange(range) <= (textView.string as NSString).length else { return }
        textView.insertText(text, replacementRange: range)
        let caret = composer.map { PastedTextStore.viewOffset(caret, tokens: $0.tokenLocations) } ?? caret
        textView.setSelectedRange(NSRange(location: min(caret, (textView.string as NSString).length), length: 0))
    }
}
