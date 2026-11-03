@testable import AgenthailHelpers
import Foundation

final class DaemonDropProtocol: URLProtocol, @unchecked Sendable {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var open: [DaemonDropProtocol] = []
    nonisolated(unsafe) private static var failing = 0
    nonisolated(unsafe) private static var down = false
    nonisolated(unsafe) private static var refused = false

    static func set(failing count: Int = 0, down isDown: Bool = false, refused isRefused: Bool = false) {
        lock.withLock { failing = count; down = isDown; refused = isRefused }
    }

    static var streaming: Bool { lock.withLock { !open.isEmpty } }

    static func dropStreams() {
        let dropped = lock.withLock { defer { open = [] }; return open }
        for stream in dropped { stream.client?.urlProtocol(stream, didFailWithError: URLError(.networkConnectionLost)) }
    }

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "daemon.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() { Self.lock.withLock { Self.open.removeAll { $0 === self } } }

    override func startLoading() {
        let url = request.url!
        let (isDown, isRefused, failProbe): (Bool, Bool, Bool) = Self.lock.withLock {
            let probe = url.path == "/api/v1/version" && Self.failing > 0
            if probe { Self.failing -= 1 }
            return (Self.down, Self.refused, probe)
        }
        if isDown || failProbe {
            client?.urlProtocol(self, didFailWithError: URLError(failProbe ? .timedOut : .networkConnectionLost))
            return
        }
        if isRefused {
            respond(401, #"{"error":{"message":"Sign in again."}}"#)
            return
        }
        switch url.path {
        case "/api/v1/events":
            Self.lock.withLock { Self.open.append(self) }
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(": connected\n\n".utf8))
        case "/api/v1/version":
            respond(200, #"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#)
        case "/api/v1/snapshot":
            respond(200, #"{"updatedAt":"2026-10-23T21:00:00Z","hostEpoch":"h","catalogSeq":1,"daemon":{"running":true,"pid":1,"stale":false},"surfaces":[],"sessions":[],"totalSessions":0,"queue":[],"channels":[],"relays":[],"history":[],"attention":[],"codexRecentHours":5,"busyDelivery":"queue"}"#)
        default:
            respond(200, "{}")
        }
    }

    private func respond(_ status: Int, _ body: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct ConnectionGraceTest {
    @MainActor
    static func main() {
        var grace = ConnectionGrace()
        let now = Date()
        check(!grace.holds(URLError(.timedOut), at: now), "no contact yet holds nothing")
        grace.contact(at: now)
        check(grace.holds(URLError(.timedOut), at: now.addingTimeInterval(5)), "a timeout soon after contact is held")
        check(grace.holds(URLError(.networkConnectionLost), at: now.addingTimeInterval(5)), "a lost connection is held")
        check(grace.holds(AgenthailAPIError.streamClosed, at: now.addingTimeInterval(5)), "a closed stream is held")
        check(!grace.holds(URLError(.timedOut), at: now.addingTimeInterval(grace.window)), "past the window nothing is held")
        check(!grace.holds(AgenthailAPIError.request(401, "no"), at: now), "a refusal is never held")
        check(!grace.holds(AgenthailAPIError.incompatible(2), at: now), "an incompatible daemon is never held")
        check(!grace.holds(URLError(.serverCertificateUntrusted), at: now), "a TLS failure is never held")
        grace.forget()
        check(!grace.holds(URLError(.timedOut), at: now), "a forgotten contact holds nothing")

        setenv("AGENTHAIL_API_URL", "http://daemon.test", 1)
        setenv("AGENTHAIL_API_TOKEN", "t", 1)
        URLProtocol.registerClass(DaemonDropProtocol.self)

        let model = AgenthailModel(connecting: true)
        check(wait(within: 10) { model.isConnected && DaemonDropProtocol.streaming }, "the Mac app connects")

        DaemonDropProtocol.set(failing: 1)
        DaemonDropProtocol.dropStreams()
        check(holds(for: 3) { model.isConnected }, "one failed probe after a stream drop keeps the Mac app connected: \(model.connectionError ?? "")")
        check(wait(within: 5) { DaemonDropProtocol.streaming }, "the stream comes back")

        model.grace.window = 1
        DaemonDropProtocol.set(down: true)
        DaemonDropProtocol.dropStreams()
        check(wait(within: 6) { !model.isConnected }, "a daemon down past the window shows disconnected")

        DaemonDropProtocol.set()
        check(wait(within: 25) { model.isConnected }, "the Mac app reconnects")
        model.grace.window = 15
        DaemonDropProtocol.set(refused: true)
        DaemonDropProtocol.dropStreams()
        check(wait(within: 1.5) { !model.isConnected }, "a refusal shows at once")
        print("connection grace tests passed")
    }

    @MainActor
    static func wait(within seconds: TimeInterval, _ condition: () -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while !condition() {
            if Date() > deadline { return false }
            RunLoop.main.run(until: Date().addingTimeInterval(0.01))
        }
        return true
    }

    @MainActor
    static func holds(for seconds: TimeInterval, _ condition: () -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(seconds)
        while Date() < deadline {
            if !condition() { return false }
            RunLoop.main.run(until: Date().addingTimeInterval(0.01))
        }
        return true
    }

    static func check(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
