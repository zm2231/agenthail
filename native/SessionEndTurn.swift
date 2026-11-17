import Foundation

struct SessionEndTurn: Codable, Hashable {
    var headline: String? = nil
    var endedAt: String? = nil
}

/// How the daemon reads the session's last turn. Absent until a turn is judged; then the row's status stands.
struct SessionTurnState: Codable, Hashable {
    var state: String
    var reason: String? = nil
    var source: String? = nil
    var turnId: String? = nil
    var pushed: Bool? = nil

    var waitsOnPerson: Bool { state == "waiting_person" }
}

struct SessionAsk: Codable, Hashable {
    var id: String
    var title: String
    var routedTo: String
    var askedAt: String? = nil
    var answeredAt: String? = nil
    var open: [String]? = nil

    var waitsOnPerson: Bool { !id.isEmpty && routedTo == "person" && (answeredAt ?? "").isEmpty }
}

extension SessionState {
    var pendingAskID: String? {
        guard let ask, ask.waitsOnPerson else { return nil }
        return ask.id
    }

    var pendingAsks: [String]? { pendingAskID == nil ? nil : ask?.open }
}

extension SessionRequests {
    // A read can still list an ask that the row, or an answer just sent, already retired; a row from an older daemon names only its oldest.
    func offered(pendingAsk: String?, open: [String]? = nil, retired: String? = nil) -> [SessionRequest] {
        guard let pendingAsk else { return requests.filter { !$0.isAsk } }
        let waiting = Set(open ?? [pendingAsk])
        return requests.filter { !$0.isAsk || (waiting.contains($0.id) && $0.id != retired) }
    }
}

struct AnswerReceipt: Hashable {
    let text: String
    let at: Date

    init(_ request: SessionRequest, _ decision: RequestDecision, at: Date = Date()) {
        let answers = (request.questions ?? []).compactMap { question -> String? in
            if let typed = decision.answers?[question.question] { return typed }
            let picked = question.options.filter { decision.choices.contains($0.id) }.map(\.label)
            return picked.isEmpty ? nil : picked.joined(separator: ", ")
        }
        let note = (decision.message ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        text = "You answered: " + (answers + (note.isEmpty ? [] : [note])).joined(separator: " · ")
        self.at = at
    }
}

// One entry per session, kept while the row still names the ask, so it can neither grow nor hide a newer ask.
struct RetiredAsks: Hashable {
    private struct Entry: Hashable {
        let requestID: String
        let receipt: AnswerReceipt?
    }

    private var bySession: [String: Entry] = [:]

    func retired(in sessionID: String) -> String? { bySession[sessionID]?.requestID }

    func receipt(for session: SessionState) -> AnswerReceipt? {
        guard let entry = bySession[session.id], entry.requestID == session.ask?.id else { return nil }
        return entry.receipt
    }

    mutating func retire(_ requestID: String, in sessionID: String, receipt: AnswerReceipt? = nil) {
        bySession[sessionID] = Entry(requestID: requestID, receipt: receipt)
    }

    func settles(_ session: SessionState) -> Bool {
        bySession[session.id].map { $0.requestID != session.ask?.id } ?? false
    }

    mutating func settle(_ session: SessionState) {
        if settles(session) { bySession[session.id] = nil }
    }
}

extension RequestAnswerFailure {
    static func retires(_ request: SessionRequest, failure: RequestAnswerFailure?) -> Bool {
        request.isAsk && (failure == nil || failure?.dropsCard == true)
    }
}

enum RequestSync {
    static func lists(_ session: SessionState) -> Bool {
        !session.isReadOnly && (session.capabilities.respond || session.pendingAskID != nil)
    }

    static func cacheKey(_ session: SessionState) -> String {
        "\(session.id)|\(session.waitingOn?.id ?? "")|\(session.pendingAskID ?? "")|\((session.pendingAsks ?? []).joined(separator: ","))"
    }

    static func viewKey(_ session: SessionState) -> String {
        "\(cacheKey(session))|\(session.capabilities.respond)|\(session.isReadOnly)"
    }
}
