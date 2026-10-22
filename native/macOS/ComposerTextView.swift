import AppKit
import SwiftUI

struct ComposerTextView: NSViewRepresentable {
    @Binding var text: String
    @Binding var focused: Bool
    let placeholder: String
    var accessibilityLabel: String? = nil
    var lines: ClosedRange<Int> = 2...10
    var onPaste: ((PastedAttachments) -> Void)? = nil
    let onSubmit: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let textView = ComposerNSTextView.make()
        textView.delegate = context.coordinator
        textView.placeholder = placeholder
        textView.setAccessibilityPlaceholderValue(placeholder)
        textView.setAccessibilityLabel(accessibilityLabel ?? placeholder)
        textView.string = text
        textView.command = { ComposerCommand.matching($0, in: .shared)?.selector }
        textView.onSubmit = { [weak coordinator = context.coordinator] in coordinator?.parent.onSubmit() }
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
        if textView.placeholder != placeholder {
            textView.placeholder = placeholder
            textView.setAccessibilityPlaceholderValue(placeholder)
        }
        textView.setAccessibilityLabel(accessibilityLabel ?? placeholder)
        if textView.string != text, !textView.hasMarkedText() {
            textView.replaceAll(with: text)
        }
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

        init(_ parent: ComposerTextView) {
            self.parent = parent
        }

        func textDidChange(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            textView.needsDisplay = true
            if parent.text != textView.string { parent.text = textView.string }
        }

        func focusChanged(_ isFocused: Bool) {
            DispatchQueue.main.async { [self] in
                if parent.focused != isFocused { parent.focused = isFocused }
            }
        }
    }
}
