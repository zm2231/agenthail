import Foundation

extension SessionDetail {
    func sessionState(open: Bool, host: SessionHost? = nil) -> SessionState {
        var state = SessionState(id: session.id, surface: session.surface, name: session.name, alias: alias.flatMap { $0.isEmpty ? nil : $0 },
                                 status: session.status, lastActive: session.lastActive, queueCount: 0, open: open, current: false,
                                 currentReason: nil, capabilities: capabilities, readOnly: readOnly, readOnlyReason: readOnlyReason, cwd: session.cwd)
        state.runtime = session.runtime
        state.runtimeKind = session.runtimeKind
        state.transport = session.transport
        state.waitingOn = session.waitingOn
        state.backgroundTasks = session.backgroundTasks
        state.usageLimit = session.usageLimit
        state.limitResume = limitResume
        state.activity = session.activity
        state.host = host
        return state
    }

    var orchestratorState: String? {
        let state = sessionState(open: true)
        return SessionDisplayKind.of(state) == .idle ? nil : SessionDisplayKind.label(state, waitingPlace: waitingPlace, limit: limitLabel)
    }
}
