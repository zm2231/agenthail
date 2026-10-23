import UIKit
import UniformTypeIdentifiers

extension ComposerAttachments {
    // Nil means nothing on the pasteboard is an attachment, so Paste stays a text paste.
    static func pasted(from pasteboard: UIPasteboard) -> PastedAttachments? {
        var pasted = PastedAttachments()
        for index in 0 ..< pasteboard.numberOfItems {
            let item = IndexSet(integer: index)
            let types = pasteboard.types(forItemSet: item)?.first ?? []
            let text = types.first { UTType($0)?.conforms(to: .plainText) == true }.flatMap { type in
                let value = pasteboard.values(forPasteboardType: type, inItemSet: item)?.first
                return value as? String ?? (value as? Data).flatMap { String(data: $0, encoding: .utf8) }
            }
            guard let type = pastedType(types, text: text) else { continue }
            pasted.add(pasteboard.data(forPasteboardType: type, inItemSet: item)?.first, type: type)
        }
        return pasted.isEmpty ? nil : pasted
    }
}
