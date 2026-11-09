import XCTest
@testable import Agenthail

@MainActor
final class HostGroupsTests: XCTestCase {
    private func makeModel() throws -> AgenthailIOSModel {
        GroupActionProtocol.state.reset()
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [GroupActionProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "https://studio.invalid")!, token: "test", session: URLSession(configuration: configuration))
        let model = AgenthailIOSModel(api: api, paired: true, hostID: "hstudio", hostName: "studio")
        let json = #"""
        {"updatedAt":"now","daemon":{"running":true},"surfaces":[],"totalSessions":2,"queue":[],"history":[],"attention":[],"codexRecentHours":24,
         "sessions":[
          {"id":"lead","surface":"claude","name":"lead","status":"idle","queueCount":0,"open":true,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}},
          {"id":"tests","surface":"codex","name":"tests","status":"idle","queueCount":0,"open":true,"current":true,"readOnly":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}}],
         "channels":[{"name":"release","members":["lead"],"memberDetails":[{"id":"lead","display":"@lead"}]}],
         "relays":[{"id":7,"from":"lead","to":"tests","pattern":".*","once":true,"active":true,"handoff":"turn_finished","pendingCount":0,"fireCount":1}]}
        """#
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: Data(json.utf8)))
        return model
    }

    func testEachComputerKeepsItsOwnChannelsAndHandoffs() throws {
        let model = try makeModel()
        let groups = try XCTUnwrap(model.hostGroups["hstudio"])
        XCTAssertEqual(groups.channels.map(\.name), ["release"])
        XCTAssertEqual(groups.relays.map(\.id), [7])
        XCTAssertEqual(RelayFacts.state(groups.relays[0]), "Turn finished · Once · 0 pending · active · fired once")
        XCTAssertEqual(model.groupName("lead", on: "hstudio"), "lead")
        XCTAssertEqual(model.groupSessions(on: "hstudio", writableOnly: false).map(\.id), ["lead", "tests"])
        XCTAssertEqual(model.groupSessions(on: "hstudio", writableOnly: true).map(\.id), ["lead"], "a read-only session cannot be handed work")
    }

    func testChannelAndHandoffActionsGoToTheirComputerAsItKnowsThem() async throws {
        let model = try makeModel()
        try await model.groupAction(on: "hstudio", "channel-add", channel: "release", targetID: "tests")
        try await model.groupAction(on: "hstudio", "relay-add", fromID: "lead", toID: "tests", pattern: "done", once: false, handoff: "goal_completed")
        try await model.groupAction(on: "hstudio", "relay-remove", relayID: 7)
        let sent = try await model.sendToChannel("release", on: "hstudio", message: "ship it", idempotencyKey: "k1")

        let bodies = GroupActionProtocol.state.bodies
        XCTAssertEqual(bodies.map { $0["action"] as? String }, ["channel-add", "relay-add", "relay-remove", "channel-send"])
        XCTAssertEqual(bodies[0]["channel"] as? String, "release")
        XCTAssertEqual(bodies[0]["targetId"] as? String, "tests")
        XCTAssertEqual(bodies[1]["fromId"] as? String, "lead")
        XCTAssertEqual(bodies[1]["handoff"] as? String, "goal_completed")
        XCTAssertEqual(bodies[1]["once"] as? Bool, false)
        XCTAssertEqual((bodies[2]["relayId"] as? NSNumber)?.int64Value, 7)
        XCTAssertEqual(bodies[3]["channel"] as? String, "release")
        XCTAssertEqual(sent, "Sent to 1: lead.")
    }

    func testAnUnpairedComputerTakesNoAction() async {
        let model = AgenthailIOSModel(autoConnect: false)
        do {
            try await model.groupAction(on: "hgone", "channel-delete", channel: "release")
            XCTFail("an action reached an unpaired computer")
        } catch {
            XCTAssertEqual(AgenthailAPIError.userMessage(error), "Connect to this computer first.")
        }
    }
}

private final class GroupActionProtocol: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let lock = NSLock()
        private var recorded: [[String: Any]] = []
        var bodies: [[String: Any]] { lock.withLock { recorded } }
        func reset() { lock.withLock { recorded = [] } }
        func record(_ body: [String: Any]) { lock.withLock { recorded.append(body) } }
    }

    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        guard request.url?.path == "/api/v1/actions", let data = Self.body(request),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return respond(404, #"{"error":"not found"}"#)
        }
        Self.state.record(json)
        respond(200, json["action"] as? String == "channel-send" ? #"{"recipients":[{"sessionId":"lead","outcome":"delivered"}]}"# : "{}")
    }

    override func stopLoading() {}

    private static func body(_ request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let count = stream.read(&buffer, maxLength: buffer.count)
            guard count > 0 else { break }
            data.append(buffer, count: count)
        }
        return data
    }

    private func respond(_ status: Int, _ body: String) {
        let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}
