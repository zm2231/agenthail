import XCTest
@testable import Agenthail

final class InboxStopsTests: XCTestCase {
    @MainActor
    func testThePhoneCountsEveryStopOnItsProjectAndShowsADeliveryOnce() throws {
        var snapshot = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as? [String: Any])
        var sessions = try XCTUnwrap(snapshot["sessions"] as? [[String: Any]])
        sessions[0]["project"] = ["id": "p-api", "displayName": "api", "rootPath": "/p/api", "kind": "git"]
        snapshot["sessions"] = sessions
        let demo = try XCTUnwrap(sessions[0]["id"] as? String)
        snapshot["projects"] = [["id": "p-api", "displayName": "api", "checkouts": []]]
        snapshot["attention"] = [["id": 1, "sessionId": demo, "target": "fix the build", "queueId": 7, "reason": "Delivery failed", "requestedAction": "Retry or cancel this message", "createdAt": "2026-11-08T10:00:00Z"]]
        snapshot["deliveryProblems"] = [["deliveryId": 3, "sessionId": demo, "message": "check the schema", "reason": "target_gone", "status": "failed", "at": "2026-11-08T10:05:00Z"]]
        let model = AgenthailIOSTests.computerModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: JSONSerialization.data(withJSONObject: snapshot)))

        let stops = model.inboxStops
        XCTAssertEqual(stops.stopped.map(\.id), ["queue:computer/7", "delivery:computer/3"], "rows merged from a computer carry its id")
        XCTAssertEqual(stops.needsYou(in: model.qualifiedProjects, projectOf: model.projectOfSession()), ["computer::p-api": 2])
        XCTAssertEqual(stops.count, 2)

        var outgoing = DecisionItem(group: .stopped, sessionId: "computer::" + demo, projectId: "computer::p-api", kind: "outgoing", reason: "1 message not delivered", answerable: false)
        outgoing.hostID = "computer"
        let shown = InboxStops.build(attention: [], deliveries: model.deliveryProblems, replacements: [], inbox: DecisionInbox(decisions: [outgoing]), localHosts: Set(model.hosts.map(\.id)))
        XCTAssertTrue(shown.decisions.isEmpty, "a paired computer's delivery row stands in for its outgoing decision")
        XCTAssertEqual(shown.count, 1)
    }

    func testInboxStopsMatchTheMacAndDashboardVectors() throws {
        struct HostTag: Decodable { let hostId: String? }
        struct Case: Decodable {
            let name: String
            let localHosts: [String]
            let projects: [ProjectState]
            let sessions: [String: String]
            let attention: [AttentionState]
            let deliveries: [DeliveryProblem]
            let replacements: [LeadReplacement]
            let decisions: [DecisionItem]
            let stopped: [String]
            let decisionsShown: [String]
            let counts: [String: Int]
            let total: Int
        }
        struct Tags: Decodable { struct Case: Decodable { let decisions: [HostTag] }; let cases: [Case] }
        struct Vectors: Decodable { let cases: [Case] }
        let url = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "inbox-stop-vectors", withExtension: "json"))
        let data = try Data(contentsOf: url)
        let vectors = try JSONDecoder().decode(Vectors.self, from: data)
        let tags = try JSONDecoder().decode(Tags.self, from: data)
        for (vector, tag) in zip(vectors.cases, tags.cases) {
            let decisions = zip(vector.decisions, tag.decisions).map { item, host in
                var item = item
                item.hostID = host.hostId
                return item
            }
            let stops = InboxStops.build(attention: vector.attention, deliveries: vector.deliveries, replacements: vector.replacements,
                                         inbox: DecisionInbox(decisions: decisions), localHosts: Set(vector.localHosts))
            XCTAssertEqual(stops.stopped.map(\.id), vector.stopped, vector.name)
            XCTAssertEqual(stops.decisions.map(\.sessionId), vector.decisionsShown, vector.name)
            XCTAssertEqual(stops.needsYou(in: vector.projects) { vector.sessions[$0] }, vector.counts, vector.name)
            XCTAssertEqual(stops.count, vector.total, vector.name)
        }
    }
}
