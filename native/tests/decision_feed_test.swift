@testable import AgenthailHelpers
import SwiftUI
import Foundation

@MainActor
final class Loads {
    var key = "1"
    var started = 0
    var waiting: [CheckedContinuation<DecisionInbox, Never>] = []

    func load() async -> DecisionInbox {
        started += 1
        return await withCheckedContinuation { waiting.append($0) }
    }

    func finish(_ inbox: DecisionInbox) async {
        waiting.removeFirst().resume(returning: inbox)
        try? await Task.sleep(for: .milliseconds(20))
    }
}

@main
struct DecisionFeedTest {
    @MainActor
    static func main() async {
        func inbox(_ ids: String..., requests: [SessionRequest]? = nil) -> DecisionInbox {
            DecisionInbox(decisions: ids.map { DecisionItem(group: .waiting, sessionId: $0, kind: "request", reason: "r", answerable: true, requests: requests) })
        }
        let loads = Loads()
        let feed = DecisionFeed(key: { loads.key }, load: { await loads.load() })
        func settle() async { try? await Task.sleep(for: .milliseconds(20)) }

        feed.refresh()
        feed.refresh()
        await settle()
        check(loads.started == 1, "asking twice for one catalog position loads once")
        feed.refresh()
        await loads.finish(inbox("a"))
        check(loads.started == 1, "asking again for the position being loaded does not drop its load")
        check(feed.loaded && feed.inbox.decisions.map(\.sessionId) == ["a"], "a current load is shown")
        feed.refresh()
        await settle()
        check(loads.started == 1, "an unchanged catalog does not ask again")

        loads.key = "2"
        feed.refresh()
        await settle()
        loads.key = "3"
        feed.refresh()
        await settle()
        check(loads.started == 2, "a change during a load waits for it")
        await loads.finish(inbox("stale"))
        check(feed.inbox.decisions.map(\.sessionId) == ["a"], "a load the catalog moved past is not shown")
        check(loads.started == 3, "the change that came during the load is read after it")

        loads.key = "4"
        feed.refresh()
        await settle()
        await loads.finish(inbox("recent"))
        check(feed.inbox.decisions.map(\.sessionId) == ["recent"], "under steady change the next superseded load is shown")
        check(loads.started == 4, "each change costs one more load")
        await loads.finish(inbox("quiet"))
        check(feed.inbox.decisions.map(\.sessionId) == ["quiet"], "the quiet catalog's load is kept")
        feed.refresh()
        await settle()
        check(loads.started == 4, "the quiet load is current")

        let first = SessionRequest(id: "1", kind: "question", tool: "AskUserQuestion")
        let second = SessionRequest(id: "2", kind: "question", tool: "AskUserQuestion")
        loads.key = "5"
        feed.refresh()
        await settle()
        await loads.finish(inbox("x", "y", requests: [first, second]))
        feed.answered(first, in: "x")
        check(feed.inbox.decisions.first { $0.sessionId == "x" }?.requests?.map(\.id) == ["2"], "an answered request leaves its session's card")
        check(feed.inbox.decisions.first { $0.sessionId == "y" }?.requests?.count == 2, "the same request id on another session stays")
        feed.refresh()
        await settle()
        check(loads.started == 6, "answering asks the daemon again")
        await loads.finish(inbox("x", "y", requests: [first, second]))
        check(feed.inbox.decisions.first { $0.sessionId == "x" }?.requests?.map(\.id) == ["2"], "a request the daemon still lists stays hidden once answered")
        feed.answered(second, in: "x")
        check(feed.inbox.decisions.map(\.sessionId) == ["y"], "a session with every request answered leaves the inbox")
        feed.refresh()
        await settle()
        await loads.finish(inbox("y", requests: [first, second]))
        check(feed.count == 1, "a session the daemon stops listing is gone")
        check(!DecisionInbox.acceptsRecommended([]), "a bare Return never accepts the recommended answers")
        check(DecisionInbox.acceptsRecommended(.shift) && DecisionInbox.acceptsRecommended([.shift, .capsLock]), "Shift-Return accepts them")
        check(!DecisionInbox.acceptsRecommended([.shift, .command]) && !DecisionInbox.acceptsRecommended(.option), "other modified Returns do not")
        print("decision feed ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
