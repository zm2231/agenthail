import AppKit

final class ComposerNSTextView: NSTextView {
    static let font = NSFont.systemFont(ofSize: DesktopType.prose)

    var placeholder = ""
    var command: ((StoredShortcut) -> ComposerCommand?)?
    var onCommand: ((ComposerCommand) -> Bool)?
    var onUpFromFirstLine: (() -> Bool)?
    var onDownFromLastLine: (() -> Bool)?
    var onSubmit: (() -> Void)?
    var completionKey: ((CompletionKey) -> Bool)?
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

    /// Measures the text apart from the view: SwiftUI asks during its layout
    /// pass, and resizing or laying out the view there makes AppKit schedule
    /// another constraints pass for every keystroke until the window aborts.
    func fittingHeight(width: CGFloat, lines: ClosedRange<Int>) -> CGFloat {
        let measured = NSMutableAttributedString(string: string, attributes: [.font: Self.font])
        if string.isEmpty || string.hasSuffix("\n") { measured.append(NSAttributedString(string: " ", attributes: [.font: Self.font])) }
        let padding = (textContainer?.lineFragmentPadding ?? 0) * 2
        let bounds = measured.boundingRect(with: NSSize(width: max(width - padding - textContainerInset.width * 2, 1), height: .greatestFiniteMagnitude), options: [.usesLineFragmentOrigin, .usesFontLeading])
        let used = ceil(bounds.height + textContainerInset.height * 2)
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
              let command = command?(StoredShortcut(keyCode: event.keyCode, modifiers: event.modifierFlags)) else { return false }
        if let selector = command.selector {
            doCommand(by: selector)
            return true
        }
        return onCommand?(command) ?? false
    }

    override func doCommand(by selector: Selector) {
        switch selector {
        case #selector(insertNewline(_:)):
            if shiftHeld {
                insertNewlineIgnoringFieldEditor(nil)
            } else if selectedRange().length == 0, let backslash = ComposerReturn.continuation(in: string, caret: selectedRange().location) {
                insertText("\n", replacementRange: backslash)
            } else if completionKey?(.accept) != true {
                onSubmit?()
            }
        case #selector(moveUp(_:)):
            if completionKey?(.up) == true { return }
            if !(selectedRange().length == 0 && onFirstLine && onUpFromFirstLine?() == true) { super.doCommand(by: selector) }
        case #selector(moveDown(_:)):
            if completionKey?(.down) == true { return }
            if !(selectedRange().length == 0 && onLastLine && onDownFromLastLine?() == true) { super.doCommand(by: selector) }
        case #selector(insertLineBreak(_:)):
            insertNewlineIgnoringFieldEditor(nil)
        case #selector(insertTab(_:)):
            if completionKey?(.accept) != true { window?.selectNextKeyView(nil) }
        case #selector(insertBacktab(_:)):
            window?.selectPreviousKeyView(nil)
        case #selector(cancelOperation(_:)):
            break
        default:
            super.doCommand(by: selector)
        }
    }

    var onFirstLine: Bool {
        let caret = selectedRange().location
        guard caret > 0 else { return true }
        var actual = NSRange()
        let start = firstRect(forCharacterRange: NSRange(location: 0, length: 0), actualRange: &actual)
        let here = firstRect(forCharacterRange: NSRange(location: caret, length: 0), actualRange: &actual)
        return abs(here.midY - start.midY) < 1
    }

    var onLastLine: Bool {
        let caret = selectedRange().location
        let length = (string as NSString).length
        guard caret < length else { return true }
        var actual = NSRange()
        let end = firstRect(forCharacterRange: NSRange(location: length, length: 0), actualRange: &actual)
        let here = firstRect(forCharacterRange: NSRange(location: caret, length: 0), actualRange: &actual)
        return abs(here.midY - end.midY) < 1
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

    func show(bound text: String, sync: ComposerTextSync) {
        if let shown = sync.textToShow(bound: text), string != shown { replaceAll(with: shown) }
    }

    func replaceAll(with text: String) {
        if hasMarkedText() { unmarkText() }
        let range = NSRange(location: 0, length: (string as NSString).length)
        breakUndoCoalescing()
        if shouldChangeText(in: range, replacementString: text) {
            replaceCharacters(in: range, with: text)
            didChangeText()
        }
        breakUndoCoalescing()
        setSelectedRange(NSRange(location: (text as NSString).length, length: 0))
    }

    /// A text role the backdrop keeps at contrast.target; the system
    /// placeholder colour is too faint over glass.
    static let placeholderColor = NSColor(DesktopPalette.text2)

    override func draw(_ dirtyRect: NSRect) {
        super.draw(dirtyRect)
        guard string.isEmpty, !hasMarkedText(), !placeholder.isEmpty else { return }
        let origin = NSPoint(x: textContainerInset.width + (textContainer?.lineFragmentPadding ?? 0), y: textContainerInset.height)
        NSAttributedString(string: placeholder, attributes: [.font: font ?? Self.font, .foregroundColor: Self.placeholderColor])
            .draw(at: origin)
    }
}

enum CompletionKey {
    case up, down, accept
}
