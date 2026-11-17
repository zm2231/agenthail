import SwiftUI
import UIKit

final class PasteTokenAttachment: NSTextAttachment, PasteTokenMarking {
    let marker: String

    @MainActor init(marker: String, font: UIFont, traits: UITraitCollection) {
        self.marker = marker
        super.init(data: nil, ofType: nil)
        refresh(font: font, traits: traits)
    }

    required init?(coder: NSCoder) { nil }

    @MainActor
    func refresh(font: UIFont, traits: UITraitCollection) {
        let label = PastedTextStore.label(PastedTextStore.shared.text(for: marker) ?? "")
        let text = NSAttributedString(string: label, attributes: [
            .font: UIFont.systemFont(ofSize: font.pointSize * 0.86, weight: .medium),
            .foregroundColor: UIColor.label.resolvedColor(with: traits),
        ])
        let size = CGSize(width: ceil(text.size().width) + 16, height: ceil(font.lineHeight) + 2)
        let format = UIGraphicsImageRendererFormat(for: traits)
        image = UIGraphicsImageRenderer(size: size, format: format).image { _ in
            let shape = UIBezierPath(roundedRect: CGRect(origin: .zero, size: size).insetBy(dx: 0.5, dy: 0.5), cornerRadius: 6)
            UIColor.label.resolvedColor(with: traits).withAlphaComponent(0.08).setFill()
            shape.fill()
            UIColor.separator.resolvedColor(with: traits).setStroke()
            shape.stroke()
            text.draw(at: CGPoint(x: 8, y: (size.height - text.size().height) / 2))
        }
        bounds = CGRect(x: 0, y: font.descender - 1, width: size.width, height: size.height)
    }
}

extension ComposerUITextView {
    var draftText: String {
        guard textStorage.length > 0, textStorage.string.contains("\u{FFFC}") else { return text ?? "" }
        return PastedTextStore.draft(of: textStorage)
    }

    var tokenLocations: [Int] {
        guard textStorage.string.contains("\u{FFFC}") else { return [] }
        return PastedTextStore.tokenLocations(in: textStorage)
    }

    func draftOffset(_ offset: Int) -> Int { PastedTextStore.draftOffset(offset, tokens: tokenLocations) }

    var plainAttributes: [NSAttributedString.Key: Any] {
        [.font: font ?? .preferredFont(forTextStyle: .body), .foregroundColor: UIColor.label]
    }

    func attributedDraft(_ drafted: String) -> NSAttributedString {
        let built = NSMutableAttributedString()
        for piece in PastedTextStore.shared.pieces(drafted) {
            switch piece {
            case let .plain(plain): built.append(NSAttributedString(string: plain, attributes: plainAttributes))
            case let .token(marker): built.append(token(marker))
            }
        }
        return built
    }

    func token(_ marker: String) -> NSAttributedString {
        let attachment = PasteTokenAttachment(marker: marker, font: font ?? .preferredFont(forTextStyle: .body), traits: traitCollection)
        let token = NSMutableAttributedString(attachment: attachment)
        token.addAttributes(plainAttributes, range: NSRange(location: 0, length: token.length))
        return token
    }

    func insertPastedText(_ pasted: String) {
        let inserted = PastedTextStore.shared.add(pasted).map(token) ?? NSAttributedString(string: pasted, attributes: plainAttributes)
        replaceUndoably(selectedRange, with: inserted, caret: selectedRange.location + inserted.length)
    }

    func replaceUndoably(_ range: NSRange, with replacement: NSAttributedString, caret: Int) {
        let replaced = textStorage.attributedSubstring(from: range)
        let selection = selectedRange
        let inserted = NSRange(location: range.location, length: replacement.length)
        undoManager?.registerUndo(withTarget: self) { view in
            view.replaceUndoably(inserted, with: replaced, caret: selection.location)
        }
        textStorage.replaceCharacters(in: range, with: replacement)
        selectedRange = NSRange(location: min(caret, textStorage.length), length: 0)
        typingAttributes = plainAttributes
        delegate?.textViewDidChange?(self)
    }

    var selectionHasPasteToken: Bool {
        let range = selectedRange
        guard range.length > 0 else { return false }
        var found = false
        textStorage.enumerateAttribute(.attachment, in: range) { value, _, stop in
            if value is PasteTokenAttachment { found = true; stop.pointee = true }
        }
        return found
    }

    func copyExpandedSelection() {
        let selected = PastedTextStore.draft(of: textStorage.attributedSubstring(from: selectedRange))
        UIPasteboard.general.string = PastedTextStore.shared.expand(selected)
    }

    func refreshTokens() {
        textStorage.enumerateAttribute(.attachment, in: NSRange(location: 0, length: textStorage.length)) { value, range, _ in
            guard let token = value as? PasteTokenAttachment else { return }
            token.refresh(font: font ?? .preferredFont(forTextStyle: .body), traits: traitCollection)
            textStorage.edited(.editedAttributes, range: range, changeInLength: 0)
        }
    }

    func editPasteToken(_ token: PasteTokenAttachment) {
        var presenter = window?.rootViewController
        while let shown = presenter?.presentedViewController { presenter = shown }
        let editor = UIHostingController<AnyView>(rootView: AnyView(EmptyView()))
        editor.rootView = AnyView(PasteTokenEditor(text: PastedTextStore.shared.text(for: token.marker) ?? "") { [weak self, weak editor] edited in
            if let editor, editor.presentingViewController != nil { editor.dismiss(animated: true) }
            self?.finishEditing(token, edited: edited)
        }.chromeTypeSize())
        presenter?.present(editor, animated: true)
    }

    func finishEditing(_ token: PasteTokenAttachment, edited: String) {
        var found: NSRange?
        textStorage.enumerateAttribute(.attachment, in: NSRange(location: 0, length: textStorage.length)) { value, range, stop in
            if (value as? PasteTokenAttachment) === token { found = range; stop.pointee = true }
        }
        guard let range = found, edited != PastedTextStore.shared.text(for: token.marker) else {
            becomeFirstResponder()
            return
        }
        let replacement = edited.isEmpty ? NSAttributedString() : PastedTextStore.shared.add(edited).map(self.token) ?? NSAttributedString(string: edited, attributes: plainAttributes)
        let caret = selectedRange.location > range.location ? selectedRange.location + replacement.length - range.length : selectedRange.location
        replaceUndoably(range, with: replacement, caret: max(caret, 0))
        becomeFirstResponder()
    }
}

struct PasteTokenEditor: View {
    @State var text: String
    let done: (String) -> Void

    var body: some View {
        NavigationStack {
            TextEditor(text: $text)
                .font(.body.monospaced())
                .padding(.horizontal, 12)
                .readingTypeSize()
                .navigationTitle(PastedTextStore.label(text))
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .confirmationAction) { Button("Done") { done(text) } }
                }
        }
        .accessibilityIdentifier("paste-token-editor")
        .onDisappear { done(text) }
    }
}
