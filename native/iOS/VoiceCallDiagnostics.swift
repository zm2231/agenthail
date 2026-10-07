import Foundation

@MainActor
final class VoiceCallDiagnostics {
    struct Attempt: Codable {
        let id: String
        let startedAt: Date
        var milestones: [String: Double]
        var terminalCause: String?
        var failureStage: String?
        var failureCode: Int?
        var failureDomain: String?
    }
    private(set) var attempts: [Attempt] = []
    private var clocks: [String: ContinuousClock.Instant] = [:]
    private let file: URL?
    private(set) var persistenceError: String?

    init(file: URL? = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first?.appendingPathComponent("voice-attempts.json")) {
        self.file = file
        if let file, let data = try? Data(contentsOf: file), let saved = try? JSONDecoder().decode([Attempt].self, from: data) {
            attempts = Array(saved.suffix(20))
            for index in attempts.indices where attempts[index].terminalCause == nil {
                attempts[index].terminalCause = "process_terminated_or_unknown"
            }
            save()
        }
    }

    func begin(id: String) {
        clocks[id] = .now
        attempts.append(Attempt(id: id, startedAt: Date(), milestones: [:], terminalCause: nil))
        attempts = Array(attempts.suffix(20))
        clocks = clocks.filter { id, _ in attempts.contains { $0.id == id } }
        save()
    }

    func mark(_ name: String, id: String) {
        guard let index = attempts.firstIndex(where: { $0.id == id }), attempts[index].milestones[name] == nil, let started = clocks[id] else { return }
        let elapsed = started.duration(to: .now).components
        attempts[index].milestones[name] = Double(elapsed.seconds) + Double(elapsed.attoseconds) / 1e18
        save()
    }

    func end(_ cause: String, id: String) {
        guard let index = attempts.firstIndex(where: { $0.id == id }), attempts[index].terminalCause == nil else { return }
        attempts[index].terminalCause = cause
        mark("local_ended", id: id)
    }

    func failure(_ error: Error, stage: String, id: String) {
        guard let index = attempts.firstIndex(where: { $0.id == id }), attempts[index].failureStage == nil else { return }
        let error = error as NSError
        attempts[index].failureStage = stage
        attempts[index].failureCode = error.code
        let allowed = [NSURLErrorDomain, NSCocoaErrorDomain, "com.apple.CallKit.error.requesttransaction", "com.apple.CallKit.error.incomingcall"]
        attempts[index].failureDomain = allowed.contains(error.domain) ? error.domain : "application"
        mark("\(stage)_failed", id: id)
    }

    private func save() {
        guard let file else { return }
        do {
            try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
            try JSONEncoder().encode(attempts).write(to: file, options: [.atomic, .completeFileProtectionUnlessOpen])
            persistenceError = nil
        } catch { persistenceError = error.localizedDescription }
    }
}
