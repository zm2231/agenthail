import Foundation
import Observation

@MainActor
@Observable
final class DecisionFeed {
    private(set) var inbox = DecisionInbox()
    private(set) var loaded = false
    @ObservationIgnored private let load: () async -> DecisionInbox
    @ObservationIgnored private let key: () -> String
    @ObservationIgnored private var shownKey: String?
    @ObservationIgnored private var loadingKey: String?
    @ObservationIgnored private var again = false
    @ObservationIgnored private var dropped = false
    @ObservationIgnored private var fetched = DecisionInbox()
    @ObservationIgnored private var answered: Set<String> = []

    init(key: @escaping () -> String, load: @escaping () async -> DecisionInbox) {
        self.key = key
        self.load = load
    }

    var count: Int { inbox.decisions.count }

    func refresh() {
        let wanted = key()
        if shownKey == wanted { return }
        if let loadingKey {
            if loadingKey != wanted { again = true }
            return
        }
        loadingKey = wanted
        again = false
        Task { await run(wanted) }
    }

    private func run(_ wanted: String) async {
        let next = await load()
        loadingKey = nil
        let current = !again && key() == wanted
        if current || dropped {
            fetched = next
            answered.formIntersection(next.decisions.flatMap { item in item.openRequests.map { Self.mark(item.sessionId, $0) } })
            publish()
            loaded = true
            shownKey = current ? wanted : nil
            dropped = false
        } else {
            dropped = true
        }
        if again || key() != wanted {
            again = false
            refresh()
        }
    }

    func answered(_ request: SessionRequest, in sessionID: String) {
        answered.insert(Self.mark(sessionID, request))
        publish()
        shownKey = nil
    }

    private static func mark(_ sessionID: String, _ request: SessionRequest) -> String { sessionID + "\u{1}" + request.id }

    private func publish() {
        var shown = fetched
        shown.decisions = fetched.decisions.compactMap { item in
            guard let requests = item.requests, !requests.isEmpty else { return item }
            var item = item
            item.requests = requests.filter { !answered.contains(Self.mark(item.sessionId, $0)) }
            return item.requests!.isEmpty ? nil : item
        }
        if shown != inbox { inbox = shown }
    }
}
