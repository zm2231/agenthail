import SwiftUI
import UIKit

// SwiftUI's TextField only pastes text on iOS 18, so a copied image would
// need a separate button; this text view takes it from the system Paste.
struct IOSComposerTextView: UIViewRepresentable {
    @Binding var text: String
    let placeholder: String
    let label: String
    let lines: ClosedRange<Int>
    let pasteAttachments: ((PastedAttachments) -> Void)?

    func makeCoordinator() -> Coordinator { Coordinator(text: $text) }

    func makeUIView(context: Context) -> ComposerUITextView {
        let view = ComposerUITextView.make()
        view.delegate = context.coordinator
        view.accessibilityIdentifier = "composer-input"
        return view
    }

    func updateUIView(_ view: ComposerUITextView, context: Context) {
        context.coordinator.text = $text
        view.sync(text)
        view.placeholder = placeholder
        view.accessibilityLabel = label
        view.pasteAttachments = pasteAttachments
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView view: ComposerUITextView, context _: Context) -> CGSize? {
        let width = proposal.width.flatMap { $0.isFinite ? $0 : nil } ?? view.bounds.width
        return CGSize(width: width, height: view.fittingHeight(width: width, lines: lines))
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        var text: Binding<String>

        init(text: Binding<String>) { self.text = text }

        func textViewDidChange(_ view: UITextView) {
            text.wrappedValue = view.text
            (view as? ComposerUITextView)?.refreshPlaceholder()
        }
    }
}

final class ComposerUITextView: UITextView {
    var pasteAttachments: ((PastedAttachments) -> Void)?
    var placeholder = "" {
        didSet { placeholderLabel.text = placeholder }
    }
    private let placeholderLabel = UILabel()

    static func make() -> ComposerUITextView {
        let view = ComposerUITextView(usingTextLayoutManager: true)
        view.font = .preferredFont(forTextStyle: .body)
        view.adjustsFontForContentSizeCategory = true
        view.backgroundColor = .clear
        view.textContainerInset = .zero
        view.textContainer.lineFragmentPadding = 0
        view.placeholderLabel.font = view.font
        view.placeholderLabel.adjustsFontForContentSizeCategory = true
        view.placeholderLabel.textColor = .placeholderText
        view.placeholderLabel.numberOfLines = 0
        view.placeholderLabel.isAccessibilityElement = false
        view.addSubview(view.placeholderLabel)
        return view
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
        let used = min(max(measure(text ?? "", width: available), measure(lines.lowerBound, width: available)), measure(lines.upperBound, width: available))
        return ceil(used + textContainerInset.top + textContainerInset.bottom)
    }

    private func measure(_ lines: Int, width: CGFloat) -> CGFloat {
        measure(Array(repeating: "", count: max(lines, 1)).joined(separator: "\n"), width: width)
    }

    private func measure(_ string: String, width: CGFloat) -> CGFloat {
        let measured = string.isEmpty || string.hasSuffix("\n") ? string + " " : string
        let bounds = (measured as NSString).boundingRect(with: CGSize(width: width, height: .greatestFiniteMagnitude), options: [.usesLineFragmentOrigin, .usesFontLeading], attributes: [.font: font ?? .preferredFont(forTextStyle: .body)], context: nil)
        return ceil(bounds.height)
    }

    // Text set from outside waits while the keyboard is composing, so it never
    // replaces marked text mid-word.
    func sync(_ text: String) {
        guard self.text != text, markedTextRange == nil else { return }
        self.text = text
        selectedRange = NSRange(location: (text as NSString).length, length: 0)
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        if action == #selector(paste(_:)), pasteAttachments != nil, UIPasteboard.general.hasImages || UIPasteboard.general.hasURLs { return true }
        return super.canPerformAction(action, withSender: sender)
    }

    override func paste(_ sender: Any?) {
        if let pasteAttachments, let pasted = ComposerAttachments.pasted(from: UIPasteboard.general) {
            pasteAttachments(pasted)
        } else {
            super.paste(sender)
        }
    }
}
