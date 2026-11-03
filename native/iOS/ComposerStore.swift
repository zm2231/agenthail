import Foundation

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
            let hadAttachments: Bool
        }

        var drafts: [String: String] = [:]
        var stashes: [String: String] = [:]
        var sends: [Send] = []
    }

    let file: URL

    static var standard: ComposerStore? {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            .map { ComposerStore(file: $0.appendingPathComponent("composer.json")) }
    }

    func load() -> Saved {
        guard let data = try? Data(contentsOf: file), let saved = try? JSONDecoder().decode(Saved.self, from: data) else { return Saved() }
        return saved
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
        self.init(id: send.id, sessionID: send.sessionID, message: send.message, turnSettings: send.turnSettings, idempotencyKey: send.idempotencyKey, uploaded: send.uploaded, hadAttachments: !send.attachments.isEmpty)
    }

    /// Nil when attachments were not uploaded before the app ended: their bytes are gone, so this cannot be resent as it was.
    var resumable: PendingSend? {
        guard !hadAttachments || uploaded != nil else { return nil }
        return PendingSend(id: id, sessionID: sessionID, message: message, attachments: [], turnSettings: turnSettings, idempotencyKey: idempotencyKey, uploaded: uploaded ?? [], unconfirmed: true)
    }
}
