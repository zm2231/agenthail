import Foundation

struct SpooledAttachment: Codable, Equatable {
    let id: UUID
    let name: String
    let mediaType: String
    var uploaded: UploadedAttachment? = nil
}

/// What typed or sent text must outlive the app, with the bytes of attachments not yet on their computer.
struct ComposerStore {
    struct Saved: Codable, Equatable {
        struct Send: Codable, Equatable {
            let id: UUID
            let sessionID: String
            let message: String
            let idempotencyKey: String
            let uploaded: [String]?
            let wire: ActionWire?
            let hadAttachments: Bool
            var sentAt: Date? = nil
            var busyDelivery: String? = nil
            var files: [UploadedAttachment]? = nil
            var turnOptions: MessageTurnOptions? = nil
            var attachments: [SpooledAttachment]? = nil
        }

        var drafts: [String: String] = [:]
        var stashes: [String: String] = [:]
        var sends: [Send] = []
        var attachmentNames: [String: [String]] = [:]
        var uploadedAttachments: [String: [UploadedAttachment]]? = nil
        var stashedUploads: [String: [UploadedAttachment]]? = nil
        var spooledDrafts: [String: [SpooledAttachment]]? = nil
        var spooledStashes: [String: [SpooledAttachment]]? = nil

        var spooled: Set<UUID> {
            let lists = sends.compactMap(\.attachments) + Array((spooledDrafts ?? [:]).values) + Array((spooledStashes ?? [:]).values)
            return Set(lists.joined().filter { $0.uploaded == nil }.map(\.id))
        }
    }

    let file: URL
    var protectedDataIsAvailable: @MainActor () -> Bool = { true }
    // A kill mid-write leaves no file rather than a short one, so a restored attachment is whole or missing.
    var writeSpool: (Data, URL) throws -> Void = { try $0.write(to: $1, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication]) }

    var spoolFolder: URL {
        file.deletingLastPathComponent().appendingPathComponent("\(file.deletingPathExtension().lastPathComponent)-attachments", isDirectory: true)
    }

    static func standard(folder: String? = nil, protectedDataIsAvailable: @escaping @MainActor () -> Bool = { true }) -> ComposerStore? {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            .map { base in folder.map { base.appendingPathComponent($0, isDirectory: true) } ?? base }
            .map { ComposerStore(file: $0.appendingPathComponent("composer.json"), protectedDataIsAvailable: protectedDataIsAvailable) }
    }

    struct Loaded {
        var saved = Saved()
        var unreadable = false
        var setAside: URL?
        var locked = false
    }

    // Before first unlock a read fails while a rename can still succeed, so availability is taken before the read and a locked file stays put.
    @MainActor
    func load(now: Date = Date()) -> Loaded {
        guard FileManager.default.fileExists(atPath: file.path) else { return Loaded() }
        let available = protectedDataIsAvailable()
        let data = try? Data(contentsOf: file)
        if data == nil, !available { return Loaded(unreadable: true, locked: true) }
        if let data, let saved = try? JSONDecoder().decode(Saved.self, from: data) { return Loaded(saved: saved) }
        let aside = file.deletingLastPathComponent().appendingPathComponent("\(file.lastPathComponent).unreadable-\(Int64(now.timeIntervalSince1970 * 1000))")
        let moved = (try? FileManager.default.moveItem(at: file, to: aside)) != nil
        return Loaded(unreadable: true, setAside: moved ? aside : nil)
    }

    func save(_ saved: Saved) {
        guard let data = try? JSONEncoder().encode(saved) else { return }
        try? FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        guard (try? data.write(to: file, options: .atomic)) != nil else { return }
        var location = file
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try? location.setResourceValues(values)
        pruneSpool(keeping: saved.spooled)
    }

    func spool(_ attachment: ComposerAttachment) -> SpooledAttachment? {
        let record = SpooledAttachment(id: attachment.id, name: attachment.name, mediaType: attachment.mediaType, uploaded: attachment.uploaded)
        guard attachment.uploaded == nil else { return record }
        let target = spoolFolder.appendingPathComponent(attachment.id.uuidString)
        if FileManager.default.fileExists(atPath: target.path) { return record }
        do {
            try FileManager.default.createDirectory(at: spoolFolder, withIntermediateDirectories: true)
            var folder = spoolFolder
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            try? folder.setResourceValues(values)
            try writeSpool(attachment.data, target)
            return record
        } catch {
            return nil
        }
    }

    func unspool(_ record: SpooledAttachment) -> ComposerAttachment? {
        if let uploaded = record.uploaded { return ComposerAttachment(id: record.id, uploaded: uploaded) }
        guard let data = try? Data(contentsOf: spoolFolder.appendingPathComponent(record.id.uuidString)) else { return nil }
        return ComposerAttachment(id: record.id, name: record.name, data: data, mediaType: record.mediaType)
    }

    private func pruneSpool(keeping kept: Set<UUID>) {
        guard let names = try? FileManager.default.contentsOfDirectory(atPath: spoolFolder.path) else { return }
        for name in names where UUID(uuidString: name).map({ !kept.contains($0) }) ?? true {
            try? FileManager.default.removeItem(at: spoolFolder.appendingPathComponent(name))
        }
    }
}

extension ComposerStore.Saved.Send {
    init(_ send: PendingSend, spool: (ComposerAttachment) -> SpooledAttachment?) {
        let uploaded = send.uploaded ?? send.alreadyUploaded
        let spooled = uploaded == nil ? send.attachments.map(spool) : []
        self.init(id: send.id, sessionID: send.sessionID, message: send.message, idempotencyKey: send.idempotencyKey, uploaded: uploaded, wire: send.wire, hadAttachments: !send.attachments.isEmpty, sentAt: send.sentAt, busyDelivery: send.busyDelivery, files: send.files, turnOptions: send.turnOptions, attachments: spooled.isEmpty || spooled.contains(nil) ? nil : spooled.compactMap { $0 })
    }

    /// Nil when attachments were neither uploaded nor kept before the app ended: their bytes are gone, so this cannot be resent as it was.
    func resumable(unspool: (SpooledAttachment) -> ComposerAttachment?) -> PendingSend? {
        var attachments = files?.map(ComposerAttachment.init(uploaded:)) ?? []
        let uploading = hadAttachments && uploaded == nil
        if uploading {
            guard let restored = self.attachments?.map(unspool), !restored.contains(nil) else { return nil }
            attachments = restored.compactMap { $0 }
        }
        return PendingSend(id: id, sessionID: sessionID, message: message, attachments: attachments, idempotencyKey: idempotencyKey, uploaded: uploading ? nil : uploaded ?? [], wire: wire, unconfirmed: true, sentAt: sentAt ?? .distantPast, busyDelivery: busyDelivery, turnOptions: turnOptions)
    }
}
