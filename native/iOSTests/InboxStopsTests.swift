import XCTest
@testable import Agenthail

final class InboxStopsTests: XCTestCase {
    @MainActor
    func testAComputersStopsCarryItsIdAndADeliveryAskIsADecision() throws {
        var snapshot = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as? [String: Any])
        let sessions = try XCTUnwrap(snapshot["sessions"] as? [[String: Any]])
        let demo = try XCTUnwrap(sessions[0]["id"] as? String)
        snapshot["leadReplacements"] = [["id": 5, "projectId": "web", "name": "lead", "sessionId": demo, "previousSessionId": "old", "changedBy": "you", "reason": "context full", "at": "2026-11-08T10:10:00Z"]]
        let model = AgenthailIOSTests.computerModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: JSONSerialization.data(withJSONObject: snapshot)))

        var ask = DecisionItem(group: .waiting, sessionId: "computer::" + demo, projectId: nil, kind: "ask", reason: "Message not delivered", answerable: true)
        ask.hostID = "computer"
        let stops = InboxStops.build(replacements: model.leadReplacements, inbox: DecisionInbox(decisions: [ask]))
        XCTAssertEqual(stops.stopped.map(\.id), ["replacement:computer/5"], "a replacement carries its computer's id")
        XCTAssertEqual(stops.decisions.map(\.kind), ["ask"], "a delivery problem is listed once, as its ask")
        XCTAssertEqual(stops.count, 2)
    }

    func testInboxStopsMatchTheMacAndDashboardVectors() throws {
        struct HostTag: Decodable { let hostId: String? }
        struct Case: Decodable {
            let name: String
            let projects: [ProjectState]
            let sessions: [String: String]
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
            let stops = InboxStops.build(replacements: vector.replacements, inbox: DecisionInbox(decisions: decisions))
            XCTAssertEqual(stops.stopped.map(\.id), vector.stopped, vector.name)
            XCTAssertEqual(stops.decisions.map(\.sessionId), vector.decisionsShown, vector.name)
            XCTAssertEqual(stops.needsYou(in: vector.projects) { vector.sessions[$0] }, vector.counts, vector.name)
            XCTAssertEqual(stops.count, vector.total, vector.name)
        }
    }
}
