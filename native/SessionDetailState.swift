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
        state.turnStop = session.turnStop
        state.limitResume = limitResume
        state.activity = session.activity
        state.host = host
        return state
    }

    func shown(over cached: SessionState?, host: SessionHost?) -> SessionState {
        guard var row = cached else { return sessionState(open: false, host: host) }
        let live = liveState
        row.status = live.status
        (row.cwd, row.transport, row.runtime, row.runtimeKind) = (live.cwd, live.transport, live.runtime, live.runtimeKind)
        (row.waitingOn, row.backgroundTasks, row.usageLimit, row.turnStop, row.limitResume, row.activity) = (live.waitingOn, live.backgroundTasks, live.usageLimit, live.turnStop, live.limitResume, live.activity)
        return row
    }

    var orchestratorState: String? {
        let state = sessionState(open: true)
        return SessionDisplayKind.of(state) == .idle ? nil : SessionDisplayKind.label(state, waitingPlace: waitingPlace, limit: limitLabel)
    }
}
