import Foundation

@MainActor
enum DraftArchive {
    static func saved(_ drafts: [String: ComposerDraft], sends: [ComposerStore.Saved.Send], spool: (ComposerAttachment) -> SpooledAttachment?) -> ComposerStore.Saved {
        var saved = ComposerStore.Saved(sends: sends)
        var uploads: [String: [UploadedAttachment]] = [:]
        var stashedUploads: [String: [UploadedAttachment]] = [:]
        var spooledDrafts: [String: [SpooledAttachment]] = [:]
        var spooledStashes: [String: [SpooledAttachment]] = [:]
        for (session, draft) in drafts {
            if !draft.text.isEmpty { saved.drafts[session] = draft.text }
            if let stash = draft.stash, !stash.text.isEmpty { saved.stashes[session] = stash.text }
            let uploaded = draft.attachments.compactMap(\.uploaded)
            if !uploaded.isEmpty { uploads[session] = uploaded }
            let stashed = draft.stash?.attachments.compactMap(\.uploaded) ?? []
            if !stashed.isEmpty { stashedUploads[session] = stashed }
            var lost: [String] = []
            let kept = keep(draft.attachments, spool: spool, lost: &lost)
            if !kept.isEmpty { spooledDrafts[session] = kept }
            let keptStash = keep(draft.stash?.attachments ?? [], spool: spool, lost: &lost)
            if !keptStash.isEmpty { spooledStashes[session] = keptStash }
            if !lost.isEmpty { saved.attachmentNames[session] = lost }
        }
        saved.uploadedAttachments = uploads.isEmpty ? nil : uploads
        saved.stashedUploads = stashedUploads.isEmpty ? nil : stashedUploads
        saved.spooledDrafts = spooledDrafts.isEmpty ? nil : spooledDrafts
        saved.spooledStashes = spooledStashes.isEmpty ? nil : spooledStashes
        return saved
    }

    static func restore(_ saved: ComposerStore.Saved, unspool: (SpooledAttachment) -> ComposerAttachment?, into draft: (String) -> ComposerDraft) -> String? {
        var lost = saved.attachmentNames.values.flatMap { $0 }
        for (session, text) in saved.drafts { draft(session).restore(text) }
        for (session, uploads) in saved.uploadedAttachments ?? [:] {
            draft(session).restore("", attachments: uploads.map(ComposerAttachment.init(uploaded:)))
        }
        for (session, records) in saved.spooledDrafts ?? [:] {
            draft(session).restore("", attachments: restored(records, unspool: unspool, lost: &lost))
        }
        for session in Set(saved.stashes.keys).union((saved.stashedUploads ?? [:]).keys).union((saved.spooledStashes ?? [:]).keys) {
            let uploaded = (saved.stashedUploads?[session] ?? []).map(ComposerAttachment.init(uploaded:))
            let spooled = restored(saved.spooledStashes?[session] ?? [], unspool: unspool, lost: &lost)
            draft(session).keep(ComposerStash(text: saved.stashes[session] ?? "", attachments: uploaded + spooled))
        }
        return lost.isEmpty ? nil : "Agenthail closed before these files were sent. Attach them again: \(lost.joined(separator: ", "))."
    }

    private static func keep(_ attachments: [ComposerAttachment], spool: (ComposerAttachment) -> SpooledAttachment?, lost: inout [String]) -> [SpooledAttachment] {
        attachments.filter { $0.uploaded == nil }.compactMap { attachment in
            if let record = spool(attachment) { return record }
            lost.append(attachment.name)
            return nil
        }
    }

    private static func restored(_ records: [SpooledAttachment], unspool: (SpooledAttachment) -> ComposerAttachment?, lost: inout [String]) -> [ComposerAttachment] {
        records.compactMap { record in
            if let attachment = unspool(record) { return attachment }
            lost.append(record.name)
            return nil
        }
    }
}
