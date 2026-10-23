import AppKit

extension ComposerAttachments {
    // Nil means nothing on the pasteboard is an attachment, so ⌘V stays a text paste.
    static func pasted(from pasteboard: NSPasteboard) -> PastedAttachments? {
        var pasted = PastedAttachments()
        for item in pasteboard.pasteboardItems ?? [] {
            guard let type = pastedType(item.types.map(\.rawValue), text: item.string(forType: .string)) else { continue }
            pasted.add(item.data(forType: NSPasteboard.PasteboardType(type)), type: type)
        }
        return pasted.isEmpty ? nil : pasted
    }
}
