import Foundation

@MainActor
final class VoicePlayback {
    private var inputActive = false
    private var inputGeneration = 0
    private var assistantGeneration: Int?
    private var suppressed = false
    private var speakerActiveUntil = Date.distantPast
    private let setSuppressed: (Bool) -> Void

    init(setSuppressed: @escaping (Bool) -> Void) { self.setSuppressed = setSuppressed }
    var canPublish: Bool { !inputActive && assistantGeneration == nil && speakerActiveUntil <= Date() }

    func audioActivity() {
        if !suppressed { speakerActiveUntil = Date().addingTimeInterval(0.5) }
    }

    func inputStarted(speechPending: Bool) {
        guard !inputActive else { return }
        inputActive = true
        inputGeneration += 1
        if assistantGeneration != nil || speechPending || speakerActiveUntil > Date() || suppressed { suppress(true) }
    }

    func inputFinished(speechPending: Bool, hasTranscript: Bool) {
        guard inputActive || hasTranscript else { return }
        inputStarted(speechPending: speechPending)
        inputActive = false
        releaseCurrentOutput()
    }

    func outputAdded() {
        if assistantGeneration == nil { assistantGeneration = inputGeneration }
        releaseCurrentOutput()
    }

    func outputFinished() { assistantGeneration = nil }

    private func releaseCurrentOutput() {
        if !inputActive && assistantGeneration == inputGeneration { suppress(false) }
    }

    private func suppress(_ value: Bool) {
        guard value != suppressed else { return }
        suppressed = value
        speakerActiveUntil = .distantPast
        setSuppressed(value)
    }
}
