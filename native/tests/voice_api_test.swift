@testable import AgenthailHelpers
import Foundation

@main
struct VoiceAPITest {
    @MainActor
    static func main() async throws {
        for address in ["http://127.0.0.1:7412", "http://localhost:7412", "http://[::1]:7412", "https://mac.test", "https://mac.test:8443/"] {
            check((try? VoiceAPI(endpoint: URL(string: address)!, token: "fixture")) != nil, "accepts \(address)")
        }
        for address in ["http://mac.test", "http://192.168.1.20:7412", "http://0.0.0.0:7412", "ftp://127.0.0.1", "https://user:secret@mac.test", "https://mac.test?token=secret", "https://mac.test/path", "http://127.0.0.1:7412/#peer"] {
            check((try? VoiceAPI(endpoint: URL(string: address)!, token: "fixture")) == nil, "rejects \(address)")
        }

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [VoiceFixtureProtocol.self]
        let api = try VoiceAPI(endpoint: URL(string: "http://127.0.0.1:7412")!, token: "fixture-token", session: URLSession(configuration: configuration))
        let state = try await api.action(VoiceAction(action: "select", targetId: "fixture-session"))
        check(state.phase == "ready" && state.target?.id == "fixture-session", "decodes the selected route")
        let request = VoiceFixtureProtocol.recorder.requests.last!
        check(request.url?.absoluteString == "http://127.0.0.1:7412/api/v1/voice", "posts to the v1 voice route on loopback")
        check(request.value(forHTTPHeaderField: "Authorization") == "Bearer fixture-token", "authenticates with the bearer token")
        check(request.httpMethod == "POST", "actions are POST")
        let peer = api.request(path: "api/v1/voice/peer")
        check(peer.url?.absoluteString == "http://127.0.0.1:7412/api/v1/voice/peer" && peer.url?.query == nil, "peer page URL carries no credential")
        check(peer.value(forHTTPHeaderField: "Authorization") == "Bearer fixture-token", "peer page request is authenticated by header")

        VoiceFixtureProtocol.status = 409
        do {
            _ = try await api.action(VoiceAction(action: "start", attemptId: "attempt", sdp: "v=0"))
            check(false, "a blocked action throws")
        } catch {
            check(error.localizedDescription == "another device owns this call", "a blocked action surfaces the host message")
        }
        print("voice api ok")
    }

    private static func check(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}

private final class VoiceRequestRecorder: @unchecked Sendable {
    private let lock = NSLock()
    private var stored: [URLRequest] = []
    func append(_ request: URLRequest) { lock.lock(); defer { lock.unlock() }; stored.append(request) }
    var requests: [URLRequest] { lock.lock(); defer { lock.unlock() }; return stored }
}

private final class VoiceFixtureProtocol: URLProtocol, @unchecked Sendable {
    static let recorder = VoiceRequestRecorder()
    nonisolated(unsafe) static var status = 200
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "127.0.0.1" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.recorder.append(request)
        let body = Self.status == 200
            ? #"{"protocol":1,"phase":"ready","events":[],"truncated":false,"occupied":false,"target":{"id":"fixture-session","name":"fixture","surface":"claude","status":"idle","lastActive":"2026-01-01T12:00:00Z"}}"#
            : #"{"error":{"code":"voice_blocked","message":"another device owns this call"}}"#
        let response = HTTPURLResponse(url: request.url!, statusCode: Self.status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
