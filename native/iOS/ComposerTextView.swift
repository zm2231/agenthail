import SwiftUI
import UIKit

// SwiftUI's TextField only pastes text on iOS 18, so a copied image would
// need a separate button; this text view takes it from the system Paste.
struct IOSComposerTextView: UIViewRepresentable {
    @Binding var text: String
    let placeholder: String
    let label: String
    let lines: ClosedRange<Int>
    var windowShare: CGFloat? = nil
    let pasteAttachments: ((PastedAttachments) -> Void)?
    var editor: IOSComposerEditor? = nil
    var onCaret: ((Int) -> Void)? = nil
    var draftID: String? = nil

    func makeCoordinator() -> Coordinator { Coordinator(text: $text) }

    func makeUIView(context: Context) -> ComposerUITextView {
        let view = ComposerUITextView.make()
        view.delegate = context.coordinator
        view.accessibilityIdentifier = "composer-input"
        view.attributedText = view.attributedDraft(text)
        context.coordinator.sync = ComposerTextSync(text)
        view.draftID = draftID
        editor?.textView = view
        return view
    }

    func updateUIView(_ view: ComposerUITextView, context: Context) {
        context.coordinator.text = $text
        context.coordinator.onCaret = onCaret
        view.draftID = draftID
        editor?.textView = view
        view.show(bound: text, sync: context.coordinator.sync)
        view.placeholder = placeholder
        view.accessibilityLabel = label
        view.pasteAttachments = pasteAttachments
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView view: ComposerUITextView, context _: Context) -> CGSize? {
        let width = proposal.width.flatMap { $0.isFinite ? $0 : nil } ?? view.bounds.width
        var lines = lines
        if let windowShare, let height = view.window?.bounds.height, let font = view.font {
            lines = lines.lowerBound...max(lines.lowerBound, Int(height * windowShare / font.lineHeight))
        }
        return CGSize(width: width, height: view.fittingHeight(width: width, lines: lines))
    }

    @MainActor
    final class Coordinator: NSObject, UITextViewDelegate {
        var text: Binding<String>
        var sync: ComposerTextSync
        var onCaret: ((Int) -> Void)?

        init(text: Binding<String>) {
            self.text = text
            sync = ComposerTextSync(text.wrappedValue)
        }

        func textViewDidChange(_ view: UITextView) {
            sync.viewChanged(to: (view as? ComposerUITextView)?.draftText ?? view.text) { if text.wrappedValue != $0 { text.wrappedValue = $0 } }
            (view as? ComposerUITextView)?.refreshPlaceholder()
            caretMoved(view)
        }

        func textViewDidChangeSelection(_ view: UITextView) { caretMoved(view) }

        // The text view also moves the caret while SwiftUI updates the view,
        // and state can't change during an update.
        private func caretMoved(_ view: UITextView) {
            let caret = (view as? ComposerUITextView)?.draftOffset(view.selectedRange.location) ?? view.selectedRange.location
            DispatchQueue.main.async { [self] in onCaret?(caret) }
        }
    }
}

final class ComposerUITextView: UITextView {
    var pasteAttachments: ((PastedAttachments) -> Void)?
    var draftID: String? {
        didSet { if draftID != oldValue { undoManager?.removeAllActions(withTarget: self) } }
    }
    var placeholder = "" {
        didSet { placeholderLabel.text = placeholder }
    }
    private let placeholderLabel = UILabel()
    private let tokenTap = UITapGestureRecognizer()
    private let tokenTapFilter = TokenTapFilter()

    static func make() -> ComposerUITextView {
        // init(usingTextLayoutManager:) skips this subclass's property initializers.
        let view = ComposerUITextView(frame: .zero, textContainer: nil)
        view.font = .preferredFont(forTextStyle: .body)
        view.adjustsFontForContentSizeCategory = true
        view.backgroundColor = .clear
        view.textContainerInset = .zero
        view.textContainer.lineFragmentPadding = 0
        view.placeholderLabel.font = view.font
        view.placeholderLabel.adjustsFontForContentSizeCategory = true
        view.placeholderLabel.textColor = UIColor { $0.userInterfaceStyle == .light ? .secondaryLabel : .placeholderText }
        view.placeholderLabel.numberOfLines = 0
        view.placeholderLabel.isAccessibilityElement = false
        view.addSubview(view.placeholderLabel)
        view.tokenTap.addTarget(view, action: #selector(tappedToken))
        view.tokenTap.delegate = view.tokenTapFilter
        view.tokenTapFilter.view = view
        view.addGestureRecognizer(view.tokenTap)
        view.registerForTraitChanges([UITraitUserInterfaceStyle.self, UITraitPreferredContentSizeCategory.self]) { (view: ComposerUITextView, _) in view.refreshTokens() }
        return view
    }

    @objc private func tappedToken() {
        guard let token = tokenHit(at: tokenTap.location(in: self)) else { return }
        resignFirstResponder()
        editPasteToken(token)
    }

    func tokenHit(at point: CGPoint) -> PasteTokenAttachment? {
        guard let closest = closestPosition(to: point) else { return nil }
        let index = offset(from: beginningOfDocument, to: closest)
        for candidate in [index - 1, index] where candidate >= 0 && candidate < textStorage.length {
            guard let token = textStorage.attribute(.attachment, at: candidate, effectiveRange: nil) as? PasteTokenAttachment,
                  let start = position(from: beginningOfDocument, offset: candidate),
                  let end = position(from: start, offset: 1),
                  let range = textRange(from: start, to: end) else { continue }
            if firstRect(for: range).insetBy(dx: -4, dy: -4).contains(point) { return token }
        }
        return nil
    }

    override var text: String! {
        didSet { refreshPlaceholder() }
    }

    func refreshPlaceholder() { placeholderLabel.isHidden = !text.isEmpty }

    override func layoutSubviews() {
        super.layoutSubviews()
        let size = placeholderLabel.sizeThatFits(CGSize(width: bounds.width, height: .greatestFiniteMagnitude))
        placeholderLabel.frame = CGRect(x: 0, y: 0, width: bounds.width, height: min(size.height, bounds.height))
    }

    /// Measures the text apart from the view, so asking during SwiftUI's
    /// layout pass never lays the view out.
    func fittingHeight(width: CGFloat, lines: ClosedRange<Int>) -> CGFloat {
        let available = max(width - textContainer.lineFragmentPadding * 2 - textContainerInset.left - textContainerInset.right, 1)
        let used = min(max(measure(attributedDraft(draftText), width: available), measure(lines.lowerBound, width: available)), measure(lines.upperBound, width: available))
        return ceil(used + textContainerInset.top + textContainerInset.bottom)
    }

    private func measure(_ lines: Int, width: CGFloat) -> CGFloat {
        measure(Array(repeating: "", count: max(lines, 1)).joined(separator: "\n"), width: width)
    }

    private func measure(_ attributed: NSAttributedString, width: CGFloat) -> CGFloat {
        let measured = NSMutableAttributedString(attributedString: attributed)
        if measured.length == 0 || measured.string.hasSuffix("\n") { measured.append(NSAttributedString(string: " ", attributes: plainAttributes)) }
        return ceil(measured.boundingRect(with: CGSize(width: width, height: .greatestFiniteMagnitude), options: [.usesLineFragmentOrigin, .usesFontLeading], context: nil).height)
    }

    private func measure(_ string: String, width: CGFloat) -> CGFloat {
        let measured = string.isEmpty || string.hasSuffix("\n") ? string + " " : string
        let bounds = (measured as NSString).boundingRect(with: CGSize(width: width, height: .greatestFiniteMagnitude), options: [.usesLineFragmentOrigin, .usesFontLeading], attributes: [.font: font ?? .preferredFont(forTextStyle: .body)], context: nil)
        return ceil(bounds.height)
    }

    // Setting the text ends a composition without telling the delegate, where
    // unmarkText would report the composed text and write it over the change.
    func show(bound text: String, sync: ComposerTextSync) {
        guard let shown = sync.textToShow(bound: text), draftText != shown else { return }
        undoManager?.removeAllActions(withTarget: self)
        attributedText = attributedDraft(shown)
        refreshPlaceholder()
        selectedRange = NSRange(location: textStorage.length, length: 0)
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        if action == #selector(paste(_:)), pasteAttachments != nil, UIPasteboard.general.hasImages || UIPasteboard.general.hasURLs { return true }
        return super.canPerformAction(action, withSender: sender)
    }

    override func paste(_ sender: Any?) {
        if let pasteAttachments, let pasted = ComposerAttachments.pasted(from: UIPasteboard.general) {
            pasteAttachments(pasted)
        } else if UIPasteboard.general.hasStrings, let text = UIPasteboard.general.string, PastedTextStore.isLong(text) {
            insertPastedText(text)
        } else {
            super.paste(sender)
        }
    }

    override func copy(_ sender: Any?) {
        if selectionHasPasteToken { copyExpandedSelection() } else { super.copy(sender) }
    }

    override func cut(_ sender: Any?) {
        guard selectionHasPasteToken else { return super.cut(sender) }
        copyExpandedSelection()
        replaceUndoably(selectedRange, with: NSAttributedString(), caret: selectedRange.location)
    }
}

private final class TokenTapFilter: NSObject, UIGestureRecognizerDelegate {
    weak var view: ComposerUITextView?

    func gestureRecognizer(_ recognizer: UIGestureRecognizer, shouldReceive touch: UITouch) -> Bool {
        guard let view else { return false }
        return view.tokenHit(at: touch.location(in: view)) != nil
    }

    func gestureRecognizer(_ recognizer: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool { true }

}

/// Replaces part of the composer's text through the text view, so the edit
/// joins its undo and the caret lands after it.
@MainActor
final class IOSComposerEditor {
    weak var textView: UITextView?

    func replace(_ range: NSRange, with text: String, caret: Int) {
        guard let textView else { return }
        let composer = textView as? ComposerUITextView
        let range = composer.map { PastedTextStore.viewRange(range, tokens: $0.tokenLocations) } ?? range
        guard NSMaxRange(range) <= (textView.text as NSString).length else { return }
        if textView.markedTextRange != nil { textView.unmarkText() }
        textView.selectedRange = range
        textView.insertText(text)
        let caret = composer.map { PastedTextStore.viewOffset(caret, tokens: $0.tokenLocations) } ?? caret
        textView.selectedRange = NSRange(location: min(caret, (textView.text as NSString).length), length: 0)
    }
}
