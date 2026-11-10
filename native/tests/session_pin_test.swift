@testable import AgenthailHelpers
import Foundation

final class PinStub: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var sent: [[String: Any]] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        var data = request.httpBody ?? Data()
        if data.isEmpty, let stream = request.httpBodyStream {
            stream.open()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count <= 0 { break }
                data.append(buffer, count: count)
            }
            stream.close()
        }
        let action = request.url?.path == "/api/v1/actions"
        if action, let body = try? JSONSerialization.jsonObject(with: data) as? [String: Any] { Self.sent.append(body) }
        let response = HTTPURLResponse(url: request.url!, statusCode: action ? 200 : 503, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data((action ? #"{"ok":true}"# : #"{"error":{"message":"not part of this test"}}"#).utf8))
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}

@main
struct SessionPinTest {
    @MainActor
    static func main() async {
        let row = #"{"id":"s1","surface":"claude","name":"builder","status":"idle","queueCount":0,"open":false,"current":true,"capabilities":{"send":true,"stream":true,"reply":true,"goal":false,"compact":true,"model":true,"interrupt":true,"steer":true}"#
        let pinned = try! JSONDecoder().decode(SessionState.self, from: Data((row + #","pinnedAt":"2026-11-10T09:00:00Z"}"#).utf8))
        let unpinned = try! JSONDecoder().decode(SessionState.self, from: Data((row + "}").utf8))
        check(pinned.isPinned && !unpinned.isPinned, "a row is pinned exactly when it carries pinnedAt")
        check(SidebarRowState(session: pinned, title: "builder") != SidebarRowState(session: unpinned, title: "builder"), "pinning re-renders the sidebar row")

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [PinStub.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://agenthail.test")!, token: "t", session: URLSession(configuration: configuration))
        let model = AgenthailModel(connecting: false, api: api)
        model.setPinned(sessionID: "hpeer::s2", true)
        await waitForActions(1)
        model.setPinned(sessionID: "s1", false)
        await waitForActions(2)
        let sent = PinStub.sent.map { "\($0["action"] as? String ?? "") \($0["sessionId"] as? String ?? "")" }
        check(sent == ["session-pin hpeer::s2", "session-unpin s1"], "Pin and Unpin send the daemon's actions for the row, a paired host's included: \(sent)")
        print("session pin tests passed")
    }

    @MainActor
    static func waitForActions(_ count: Int) async {
        for _ in 0..<500 where PinStub.sent.count < count { try? await Task.sleep(for: .milliseconds(10)) }
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
