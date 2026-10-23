import AppKit
import SwiftUI

struct ComposerTextView: NSViewRepresentable {
    @Binding var text: String
    @Binding var focused: Bool
    let placeholder: String
    var accessibilityLabel: String? = nil
    var lines: ClosedRange<Int> = 2...10
    var onPaste: ((PastedAttachments) -> Void)? = nil
    var onCommand: ((ComposerCommand) -> Bool)? = nil
    var onUpFromFirstLine: (() -> Bool)? = nil
    var onDownFromLastLine: (() -> Bool)? = nil
    var editor: ComposerEditor? = nil
    var onCaret: ((Int) -> Void)? = nil
    var completionKey: ((CompletionKey) -> Bool)? = nil
    let onSubmit: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let textView = ComposerNSTextView.make()
        textView.delegate = context.coordinator
        textView.placeholder = placeholder
        textView.setAccessibilityPlaceholderValue(placeholder)
        textView.setAccessibilityLabel(accessibilityLabel ?? placeholder)
        textView.string = text
        context.coordinator.sync = ComposerTextSync(text)
        textView.setSelectedRange(NSRange(location: (text as NSString).length, length: 0))
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
        textView.pasteAttachments = onPaste
        editor?.textView = textView
        if textView.placeholder != placeholder {
            textView.placeholder = placeholder
            textView.setAccessibilityPlaceholderValue(placeholder)
        }
        textView.setAccessibilityLabel(accessibilityLabel ?? placeholder)
        textView.show(bound: text, sync: context.coordinator.sync)
        if focused, textView.window != nil, textView.window?.firstResponder !== textView {
            DispatchQueue.main.async { textView.window?.makeFirstResponder(textView) }
        }
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView scroll: NSScrollView, context _: Context) -> CGSize? {
        guard let width = proposal.width, width > 0, let textView = scroll.documentView as? ComposerNSTextView else { return nil }
        return CGSize(width: width, height: textView.fittingHeight(width: width, lines: lines))
    }

    @MainActor
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: ComposerTextView
        var sync: ComposerTextSync

        init(_ parent: ComposerTextView) {
            self.parent = parent
            sync = ComposerTextSync(parent.text)
        }

        func textDidChange(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            textView.needsDisplay = true
            sync.viewChanged(to: textView.string) { if parent.text != $0 { parent.text = $0 } }
            caretMoved(textView)
        }

        func textViewDidChangeSelection(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            caretMoved(textView)
        }

        // The text view also moves the caret while SwiftUI updates the view,
        // when it replaces the text, and state can't change during an update.
        private func caretMoved(_ textView: NSTextView) {
            let caret = textView.selectedRange().location
            DispatchQueue.main.async { [self] in parent.onCaret?(caret) }
        }

        func focusChanged(_ isFocused: Bool) {
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
        guard let textView, NSMaxRange(range) <= (textView.string as NSString).length else { return }
        textView.insertText(text, replacementRange: range)
        textView.setSelectedRange(NSRange(location: min(caret, (textView.string as NSString).length), length: 0))
    }
}
