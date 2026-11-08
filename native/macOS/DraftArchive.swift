import Foundation

@MainActor
enum DraftArchive {
    static func saved(_ drafts: [String: ComposerDraft], sends: [ComposerStore.Saved.Send]) -> ComposerStore.Saved {
        var saved = ComposerStore.Saved(sends: sends)
        var uploads: [String: [UploadedAttachment]] = [:]
        var stashedUploads: [String: [UploadedAttachment]] = [:]
        for (session, draft) in drafts {
            if !draft.text.isEmpty { saved.drafts[session] = draft.text }
            if let stash = draft.stash, !stash.text.isEmpty { saved.stashes[session] = stash.text }
            let uploaded = draft.attachments.compactMap(\.uploaded)
            if !uploaded.isEmpty { uploads[session] = uploaded }
            let stashed = draft.stash?.attachments.compactMap(\.uploaded) ?? []
            if !stashed.isEmpty { stashedUploads[session] = stashed }
            let lost = (draft.attachments + (draft.stash?.attachments ?? [])).filter { $0.uploaded == nil }.map(\.name)
            if !lost.isEmpty { saved.attachmentNames[session] = lost }
        }
        saved.uploadedAttachments = uploads.isEmpty ? nil : uploads
        saved.stashedUploads = stashedUploads.isEmpty ? nil : stashedUploads
        return saved
    }

    static func restore(_ saved: ComposerStore.Saved, into draft: (String) -> ComposerDraft) -> String? {
        for (session, text) in saved.drafts { draft(session).restore(text) }
        for (session, uploads) in saved.uploadedAttachments ?? [:] {
            draft(session).restore("", attachments: uploads.map(ComposerAttachment.init(uploaded:)))
        }
        for session in Set(saved.stashes.keys).union((saved.stashedUploads ?? [:]).keys) {
            let attachments = (saved.stashedUploads?[session] ?? []).map(ComposerAttachment.init(uploaded:))
            draft(session).keep(ComposerStash(text: saved.stashes[session] ?? "", attachments: attachments))
        }
        let lost = saved.attachmentNames.values.flatMap { $0 }
        return lost.isEmpty ? nil : "Agenthail closed before these files were sent. Attach them again: \(lost.joined(separator: ", "))."
    }
}
