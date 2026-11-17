import AppKit
import SwiftUI

final class PasteTokenAttachment: NSTextAttachment, PasteTokenMarking {
    let marker: String

    @MainActor init(marker: String, font: NSFont) {
        self.marker = marker
        super.init(data: nil, ofType: nil)
        refresh(font: font)
    }

    required init?(coder: NSCoder) { nil }

    @MainActor
    func refresh(font: NSFont) {
        let label = PastedTextStore.label(PastedTextStore.shared.text(for: marker) ?? "")
        let text = NSAttributedString(string: label, attributes: [.font: NSFont.systemFont(ofSize: font.pointSize * 0.86, weight: .medium)])
        let size = CGSize(width: ceil(text.size().width) + 16, height: ceil(font.ascender - font.descender) + 2)
        image = NSImage(size: size, flipped: false) { rect in
            let shape = NSBezierPath(roundedRect: rect.insetBy(dx: 0.5, dy: 0.5), xRadius: 5, yRadius: 5)
            NSColor.labelColor.withAlphaComponent(0.08).setFill()
            shape.fill()
            NSColor.separatorColor.setStroke()
            shape.stroke()
            let drawn = NSMutableAttributedString(attributedString: text)
            drawn.addAttribute(.foregroundColor, value: NSColor.labelColor, range: NSRange(location: 0, length: drawn.length))
            drawn.draw(at: NSPoint(x: 8, y: (rect.height - text.size().height) / 2))
            return true
        }
        bounds = CGRect(x: 0, y: font.descender - 1, width: size.width, height: size.height)
    }
}

extension ComposerNSTextView {
    var draftText: String {
        guard let storage = textStorage, storage.containsAttachments else { return string }
        return PastedTextStore.draft(of: storage)
    }

    var tokenLocations: [Int] {
        guard let storage = textStorage, storage.containsAttachments else { return [] }
        return PastedTextStore.tokenLocations(in: storage)
    }

    func draftOffset(_ offset: Int) -> Int { PastedTextStore.draftOffset(offset, tokens: tokenLocations) }

    func viewRange(_ range: NSRange) -> NSRange { PastedTextStore.viewRange(range, tokens: tokenLocations) }

    func attributedDraft(_ text: String) -> NSAttributedString {
        let attributes: [NSAttributedString.Key: Any] = [.font: Self.font, .foregroundColor: NSColor.labelColor]
        let built = NSMutableAttributedString()
        for piece in PastedTextStore.shared.pieces(text) {
            switch piece {
            case let .plain(plain): built.append(NSAttributedString(string: plain, attributes: attributes))
            case let .token(marker): built.append(token(marker))
            }
        }
        return built
    }

    func token(_ marker: String) -> NSAttributedString {
        let token = NSMutableAttributedString(attachment: PasteTokenAttachment(marker: marker, font: Self.font))
        token.addAttributes([.font: Self.font, .foregroundColor: NSColor.labelColor], range: NSRange(location: 0, length: token.length))
        return token
    }

    func insertPastedText(_ text: String) {
        guard let marker = PastedTextStore.shared.add(text) else { return insertText(text, replacementRange: selectedRange()) }
        breakUndoCoalescing()
        insertText(token(marker), replacementRange: selectedRange())
        breakUndoCoalescing()
        typingAttributes = [.font: Self.font, .foregroundColor: NSColor.labelColor]
    }

    static func lineWidth(_ text: String) -> CGFloat {
        ceil(NSAttributedString(string: text, attributes: [.font: font]).size().width)
    }

    func pasteToken(at point: NSPoint) -> (token: PasteTokenAttachment, range: NSRange, rect: NSRect)? {
        guard let storage = textStorage, storage.containsAttachments else { return nil }
        let index = characterIndexForInsertion(at: point)
        for candidate in [index - 1, index] where candidate >= 0 && candidate < storage.length {
            guard let token = storage.attribute(.attachment, at: candidate, effectiveRange: nil) as? PasteTokenAttachment else { continue }
            let range = NSRange(location: candidate, length: 1)
            if let rect = frame(of: range), rect.insetBy(dx: -2, dy: -2).contains(point) { return (token, range, rect) }
        }
        return nil
    }

    private func frame(of range: NSRange) -> NSRect? {
        guard let layout = textLayoutManager, let content = layout.textContentManager,
              let start = content.location(content.documentRange.location, offsetBy: range.location),
              let end = content.location(start, offsetBy: range.length),
              let textRange = NSTextRange(location: start, end: end) else { return nil }
        var frame = NSRect.null
        layout.enumerateTextSegments(in: textRange, type: .standard, options: []) { _, segment, _, _ in
            frame = frame.union(segment)
            return true
        }
        guard !frame.isNull else { return nil }
        return frame.offsetBy(dx: textContainerOrigin.x, dy: textContainerOrigin.y)
    }

    func editPasteToken(_ token: PasteTokenAttachment, range: NSRange, from rect: NSRect) {
        let popover = NSPopover()
        popover.behavior = .transient
        let editor = PasteTokenEditor(text: PastedTextStore.shared.text(for: token.marker) ?? "") { [weak self, weak popover] edited in
            popover?.performClose(nil)
            self?.finishEditing(token, edited: edited)
        }
        popover.contentViewController = NSHostingController(rootView: editor)
        popover.show(relativeTo: rect, of: self, preferredEdge: .maxY)
    }

    func finishEditing(_ token: PasteTokenAttachment, edited: String) {
        guard let storage = textStorage else { return }
        var found: NSRange?
        storage.enumerateAttribute(.attachment, in: NSRange(location: 0, length: storage.length)) { value, range, stop in
            if (value as? PasteTokenAttachment) === token { found = range; stop.pointee = true }
        }
        guard let range = found, edited != PastedTextStore.shared.text(for: token.marker) else { return }
        let attributes: [NSAttributedString.Key: Any] = [.font: Self.font, .foregroundColor: NSColor.labelColor]
        let replacement = edited.isEmpty ? NSAttributedString() : PastedTextStore.shared.add(edited).map(self.token) ?? NSAttributedString(string: edited, attributes: attributes)
        let selection = selectedRange()
        breakUndoCoalescing()
        insertText(replacement, replacementRange: range)
        breakUndoCoalescing()
        typingAttributes = attributes
        let moved = selection.location > range.location ? selection.location + replacement.length - range.length : selection.location
        setSelectedRange(NSRange(location: min(max(moved, 0), (string as NSString).length), length: 0))
    }

    var selectionHasPasteToken: Bool {
        let range = selectedRange()
        guard range.length > 0, let storage = textStorage, storage.containsAttachments else { return false }
        var found = false
        storage.enumerateAttribute(.attachment, in: range) { value, _, stop in
            if value is PasteTokenAttachment { found = true; stop.pointee = true }
        }
        return found
    }

    func copyExpandedSelection() {
        guard let storage = textStorage else { return }
        let selected = PastedTextStore.draft(of: storage.attributedSubstring(from: selectedRange()))
        pasteboard.clearContents()
        pasteboard.setString(PastedTextStore.shared.expand(selected), forType: .string)
    }
}

struct PasteTokenEditor: View {
    @State var text: String
    let done: (String) -> Void

    var body: some View {
        VStack(alignment: .trailing, spacing: DesktopSpace.s) {
            TextEditor(text: $text)
                .font(.system(size: DesktopType.body, design: .monospaced))
                .frame(width: 520, height: 320)
            HStack {
                Text(PastedTextStore.label(text))
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
                Spacer()
                Button("Done") { done(text) }
                    .keyboardShortcut(.defaultAction)
            }
        }
        .padding(DesktopSpace.m)
        .onDisappear { done(text) }
    }
}
