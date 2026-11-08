import Foundation

enum ClaudeRunFacts {
    static func lines(_ run: ClaudeRunObservation) -> [String] {
        [
            run.runType.map { "Type: \($0)" },
            run.providerState.map { "Provider state: \($0)" },
            run.sessionId.map { "Session: \($0)" },
            run.resumeSessionId.map { "Resume session: \($0)" },
            TurnClock.time(run.createdAt).map { "Created \($0)" },
            TurnClock.time(run.updatedAt).map { "Updated \($0)" },
            run.recordPath.isEmpty ? nil : "Record: \(run.recordPath)",
        ]
        .compactMap { $0 }
        .filter { !$0.hasSuffix(": ") }
    }
}
