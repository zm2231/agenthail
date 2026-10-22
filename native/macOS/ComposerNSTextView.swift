import AppKit

final class ComposerNSTextView: NSTextView {
    static let font = NSFont.systemFont(ofSize: DesktopType.prose)

    var placeholder = ""
    var command: ((StoredShortcut) -> Selector?)?
    var onSubmit: (() -> Void)?
    var focusChanged: ((Bool) -> Void)?
    var pasteAttachments: ((PastedAttachments) -> Void)? {
        didSet { if (oldValue == nil) != (pasteAttachments == nil) { updateDragTypeRegistration() } }
    }
    var pasteboard = NSPasteboard.general
    private var shiftHeld = false

    static func make() -> ComposerNSTextView {
        let view = ComposerNSTextView(usingTextLayoutManager: true)
        view.isRichText = false
        view.importsGraphics = false
        view.usesFontPanel = false
        view.usesRuler = false
        view.allowsUndo = true
        view.drawsBackground = false
        view.font = font
        view.textColor = .labelColor
        view.typingAttributes = [.font: font, .foregroundColor: NSColor.labelColor]
        view.textContainerInset = .zero
        view.textContainer?.lineFragmentPadding = 0
        view.textContainer?.widthTracksTextView = true
        view.isVerticallyResizable = true
        view.isHorizontallyResizable = false
        view.autoresizingMask = [.width]
        view.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        view.isContinuousSpellCheckingEnabled = true
        view.isAutomaticSpellingCorrectionEnabled = NSSpellChecker.isAutomaticSpellingCorrectionEnabled
        view.isAutomaticTextReplacementEnabled = NSSpellChecker.isAutomaticTextReplacementEnabled
        view.isAutomaticQuoteSubstitutionEnabled = NSSpellChecker.isAutomaticQuoteSubstitutionEnabled
        view.isAutomaticDashSubstitutionEnabled = NSSpellChecker.isAutomaticDashSubstitutionEnabled
        view.isAutomaticTextCompletionEnabled = NSSpellChecker.isAutomaticTextCompletionEnabled
        view.writingToolsBehavior = .complete
        view.allowedWritingToolsResultOptions = [.plainText]
        return view
    }

    static let lineHeight = font.ascender - font.descender + font.leading

    func fittingHeight(width: CGFloat, lines: ClosedRange<Int>) -> CGFloat {
        if frame.width != width { setFrameSize(NSSize(width: width, height: frame.height)) }
        var used: CGFloat = 0
        if let layout = textLayoutManager {
            layout.ensureLayout(for: layout.documentRange)
            used = ceil(layout.usageBoundsForTextContainer.height + textContainerInset.height * 2)
        }
        return ceil(min(max(used, Self.lineHeight * CGFloat(lines.lowerBound)), Self.lineHeight * CGFloat(lines.upperBound)))
    }

    override func paste(_ sender: Any?) {
        if let pasteAttachments, let pasted = ComposerAttachments.pasted(from: pasteboard) {
            pasteAttachments(pasted)
        } else {
            super.paste(sender)
        }
    }

    static let attachmentDragTypes: [NSPasteboard.PasteboardType] = [.fileURL, .png, .tiff, NSPasteboard.PasteboardType("public.jpeg"), NSPasteboard.PasteboardType("public.heic")]

    override var acceptableDragTypes: [NSPasteboard.PasteboardType] {
        pasteAttachments == nil ? [.string] : [.string] + Self.attachmentDragTypes
    }

    func dropAttachments(from pasteboard: NSPasteboard) -> Bool {
        guard let pasteAttachments, let pasted = ComposerAttachments.pasted(from: pasteboard) else { return false }
        pasteAttachments(pasted)
        return true
    }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        attachmentDrag(sender) ? .copy : super.draggingEntered(sender)
    }

    override func draggingUpdated(_ sender: NSDraggingInfo) -> NSDragOperation {
        attachmentDrag(sender) ? .copy : super.draggingUpdated(sender)
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        dropAttachments(from: sender.draggingPasteboard) || super.performDragOperation(sender)
    }

    private func attachmentDrag(_ sender: NSDraggingInfo) -> Bool {
        pasteAttachments != nil && ComposerAttachments.pasted(from: sender.draggingPasteboard) != nil
    }

    override func keyDown(with event: NSEvent) {
        if perform(event) { return }
        shiftHeld = event.modifierFlags.contains(.shift)
        defer { shiftHeld = false }
        super.keyDown(with: event)
    }

    override func performKeyEquivalent(with event: NSEvent) -> Bool {
        if window?.firstResponder === self, perform(event) { return true }
        return super.performKeyEquivalent(with: event)
    }

    private func perform(_ event: NSEvent) -> Bool {
        guard event.type == .keyDown, !hasMarkedText(),
              let selector = command?(StoredShortcut(keyCode: event.keyCode, modifiers: event.modifierFlags)) else { return false }
        doCommand(by: selector)
        return true
    }

    override func doCommand(by selector: Selector) {
        switch selector {
        case #selector(insertNewline(_:)):
            if shiftHeld {
                insertNewlineIgnoringFieldEditor(nil)
            } else if selectedRange().length == 0, let backslash = ComposerReturn.continuation(in: string, caret: selectedRange().location) {
                insertText("\n", replacementRange: backslash)
            } else {
                onSubmit?()
            }
        case #selector(insertLineBreak(_:)):
            insertNewlineIgnoringFieldEditor(nil)
        case #selector(insertTab(_:)):
            window?.selectNextKeyView(nil)
        case #selector(insertBacktab(_:)):
            window?.selectPreviousKeyView(nil)
        case #selector(cancelOperation(_:)):
            break
        default:
            super.doCommand(by: selector)
        }
    }

    override func becomeFirstResponder() -> Bool {
        let became = super.becomeFirstResponder()
        if became { focusChanged?(true) }
        return became
    }

    override func resignFirstResponder() -> Bool {
        let resigned = super.resignFirstResponder()
        if resigned { focusChanged?(false) }
        return resigned
    }

    func replaceAll(with text: String) {
        let range = NSRange(location: 0, length: (string as NSString).length)
        breakUndoCoalescing()
        if shouldChangeText(in: range, replacementString: text) {
            replaceCharacters(in: range, with: text)
            didChangeText()
        }
        breakUndoCoalescing()
        setSelectedRange(NSRange(location: (text as NSString).length, length: 0))
    }

    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        guard string.isEmpty, !hasMarkedText(), !placeholder.isEmpty else { return }
        let origin = NSPoint(x: textContainerInset.width + (textContainer?.lineFragmentPadding ?? 0), y: textContainerInset.height)
        NSAttributedString(string: placeholder, attributes: [.font: font ?? Self.font, .foregroundColor: NSColor.placeholderTextColor])
            .draw(at: origin)
    }
}
