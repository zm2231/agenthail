import UIKit

/// What typed or sent text must outlive the app. Attachment bytes stay in memory.
struct ComposerStore {
    struct Saved: Codable, Equatable {
        struct Send: Codable, Equatable {
            let id: UUID
            let sessionID: String
            let message: String
            let turnSettings: TurnSettings
            let idempotencyKey: String
            let uploaded: [String]?
            let wire: ActionWire?
            let hadAttachments: Bool
        }

        var drafts: [String: String] = [:]
        var stashes: [String: String] = [:]
        var sends: [Send] = []
        var attachmentNames: [String: [String]] = [:]
        var uploadedAttachments: [String: [UploadedAttachment]]? = nil
        var stashedUploads: [String: [UploadedAttachment]]? = nil
    }

    let file: URL
    var protectedDataIsAvailable: @MainActor () -> Bool = { true }

    static var standard: ComposerStore? {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            .map { ComposerStore(file: $0.appendingPathComponent("composer.json"), protectedDataIsAvailable: { UIApplication.shared.isProtectedDataAvailable }) }
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
    }
}

extension ComposerStore.Saved.Send {
    init(_ send: PendingSend) {
        self.init(id: send.id, sessionID: send.sessionID, message: send.message, turnSettings: send.turnSettings, idempotencyKey: send.idempotencyKey, uploaded: send.uploaded ?? send.alreadyUploaded, wire: send.wire, hadAttachments: !send.attachments.isEmpty)
    }

    /// Nil when attachments were not uploaded before the app ended: their bytes are gone, so this cannot be resent as it was.
    var resumable: PendingSend? {
        guard !hadAttachments || uploaded != nil else { return nil }
        return PendingSend(id: id, sessionID: sessionID, message: message, attachments: [], turnSettings: turnSettings, idempotencyKey: idempotencyKey, uploaded: uploaded ?? [], wire: wire, unconfirmed: true)
    }
}
