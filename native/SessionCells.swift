import Observation

@MainActor
@Observable
final class SessionCell {
    var state: SessionState

    init(_ state: SessionState) { self.state = state }
}

// One observed cell per session, so a view reading one session is not told about the others.
@MainActor
@Observable
final class SessionCells {
    @ObservationIgnored private var cells: [String: SessionCell] = [:]
    private var ids: Set<String> = []

    func session(_ id: String?) -> SessionState? {
        guard let id else { return nil }
        _ = ids
        return cells[id]?.state
    }

    func adopt(_ sessions: [SessionState]) {
        var next: [String: SessionCell] = [:]
        for session in sessions where next[session.id] == nil {
            if let cell = cells[session.id] {
                cell.state = session
                next[session.id] = cell
            } else {
                next[session.id] = SessionCell(session)
            }
        }
        cells = next
        ids = Set(next.keys)
    }

    func title(of session: SessionState) -> String {
        SessionFamilies.title(session) { self.session($0) }
    }

    func title(of id: String) -> String? {
        session(id).map(title(of:))
    }
}
