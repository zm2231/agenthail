import Foundation

enum LeadCrew {
    static func crew(of id: String, in tree: SessionTree) -> [SessionState] {
        let families = tree.projects.flatMap { $0.checkouts.flatMap(\.families) } + tree.flat
        for family in families {
            if family.id == id { return family.crew.map(\.session) }
            guard let index = family.crew.firstIndex(where: { $0.id == id }) else { continue }
            let depth = family.crew[index].depth
            return Array(family.crew[(index + 1)...].prefix { $0.depth > depth }.map(\.session))
        }
        return []
    }

    static func ordered(_ crew: [SessionState], now: Date = Date()) -> [SessionState] {
        func rank(_ session: SessionState) -> Int {
            switch SessionDisplayKind.of(session, now: now) {
            case .waiting: 0
            case .working, .finishing: 1
            default: 2
            }
        }
        return crew.enumerated().sorted { a, b in
            let (ra, rb) = (rank(a.element), rank(b.element))
            if ra != rb { return ra < rb }
            let (la, lb) = (a.element.lastActive.flatMap(TimestampParser.date), b.element.lastActive.flatMap(TimestampParser.date))
            switch (la, lb) {
            case let (la?, lb?) where la != lb: return la > lb
            case (.some, nil): return true
            case (nil, .some): return false
            default: return a.offset < b.offset
            }
        }.map(\.element)
    }

    static func needsYou(_ crew: [SessionState]) -> Int {
        crew.filter(\.isWaitingOnYou).count
    }

    static func stateLabel(_ session: SessionState, now: Date = Date()) -> String {
        guard SessionDisplayKind.of(session, now: now) == .idle, let turn = session.turnState else {
            return SessionDisplayKind.label(session, now: now)
        }
        switch turn.state {
        case "done": return "Done"
        case "waiting_person": return "Needs you"
        case "continuing": return "Said it would continue"
        case "waiting_work": return turn.reason.map { $0.isEmpty ? "Waiting" : "Waiting on " + $0 } ?? "Waiting"
        default: return SessionDisplayKind.label(session, now: now)
        }
    }

    static func headline(_ session: SessionState) -> String? {
        let text = session.isWorking ? session.livePlan?.current ?? session.endTurn?.headline : session.endTurn?.headline
        return text.flatMap { $0.isEmpty ? nil : $0 }
    }

    static func stepsThisTurn(_ items: [TimelineItem]) -> [String] {
        let start = items.lastIndex(where: TranscriptFocus.startsTurn).map { $0 + 1 } ?? 0
        return items[start...].filter { ToolRunSummary.isCall($0) && $0.agent == nil && !TranscriptFocus.isEndTurn($0.title) }.map { call in
            let presentation = ToolPresentation(name: call.title, text: call.text)
            let object = presentation.object.text
            return object.isEmpty ? presentation.verb : presentation.verb + " " + object
        }
    }
}
