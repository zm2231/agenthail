@testable import AgenthailHelpers
import Foundation

final class SessionStreamProtocol: URLProtocol, @unchecked Sendable {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var bodies: [String] = []
    nonisolated(unsafe) private static var opened: [String] = []

    static func serve(_ streams: [String]) { lock.withLock { bodies = streams; opened = [] } }
    static var requests: [String] { lock.withLock { opened } }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        let body: String = Self.lock.withLock {
            Self.opened.append(url.query ?? "")
            return Self.bodies.isEmpty ? "" : Self.bodies.removeFirst()
        }
        let response = HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "text/event-stream"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        let held = body.hasSuffix("hold")
        client?.urlProtocol(self, didLoad: Data((held ? String(body.dropLast(4)) : body).utf8))
        if !held { client?.urlProtocolDidFinishLoading(self) }
    }
}

actor ReceivedRows {
    var rows: [SessionStreamEvent] = []
    func add(_ row: SessionStreamEvent) { rows.append(row) }
}

@main
struct SessionStreamRebuildTest {
    static func wire(_ json: String) -> SessionStreamWire {
        try! JSONDecoder().decode(SessionStreamWire.self, from: Data(json.utf8))
    }

    static func whole(_ seq: Int, _ body: String, id: String = "reply") -> String {
        #"{"stream":"session","sessionId":"S","seq":\#(seq),"type":"item","data":{"itemId":"\#(id)","version":1,"op":"upsert","kind":"text","role":"assistant","ts":"2026-11-06T00:00:00Z","body":"\#(body)","truncated":true,"bodyRef":"ref"},"item":{"id":"\#(id)","kind":"text","role":"assistant","title":"text","text":"\#(body)","timestamp":"2026-11-06T00:00:00Z","truncated":true,"bodyRef":"ref"}}"#
    }

    static func append(_ seq: Int, from: Int, _ text: String, id: String = "reply") -> String {
        #"{"stream":"session","sessionId":"S","seq":\#(seq),"type":"append","itemId":"\#(id)","from":\#(from),"text":"\#(text)","version":\#(seq),"ts":"2026-11-06T00:00:0\#(seq % 10)Z"}"#
    }

    static func event(_ json: String) -> String { "event: item\ndata: \(json)\n\n" }

    static func main() async {
        let inline = String(repeating: "x", count: 16384)
        var rebuild = SessionStreamRebuild()
        guard case .resync = rebuild.apply(wire(append(1, from: 0, "early"))) else { return fail("an append for a row this connection never saw asks to resync") }
        guard case .row = rebuild.apply(wire(whole(2, inline))) else { return fail("a whole row applies") }
        guard case .row(let grown) = rebuild.apply(wire(append(3, from: 16384, "é and more"))) else { return fail("an append that continues the row applies") }
        guard case .row(let again) = rebuild.apply(wire(append(4, from: 16384 + 11, " 🙂"))) else { return fail("appends apply in order, counting UTF-8 bytes") }
        check(grown.data.body == inline + "é and more" && grown.seq == 3, "the first append extends the body")
        check(again.data.body == inline + "é and more 🙂" && again.item?.text == again.data.body, "a reply past the inline body keeps growing, in its item too")
        check(!again.data.truncated && again.item?.truncated == false, "a row holding every byte is not marked cut")
        check(again.item?.timestamp == "2026-11-06T00:00:04Z" && again.data.version == 4, "an append carries the row's time and version")
        guard case .resync = rebuild.apply(wire(append(5, from: 7, "wrong"))) else { return fail("an append from the wrong offset asks to resync") }

        var bounded = SessionStreamRebuild()
        _ = bounded.apply(wire(whole(1, "a", id: "old")))
        _ = bounded.apply(wire(whole(2, "a", id: "live")))
        for index in 0..<(2 * SessionStreamRebuild.rowLimit) {
            _ = bounded.apply(wire(whole(3 + 2 * index, "a", id: "row-\(index)")))
            guard case .row = bounded.apply(wire(append(4 + 2 * index, from: 1 + index, "b", id: "live"))) else { return fail("a live row keeps growing after \(index) other rows") }
            check(bounded.count <= SessionStreamRebuild.rowLimit, "the rebuild holds \(bounded.count) rows, past its limit")
        }
        guard case .resync = bounded.apply(wire(append(100_000, from: 1, "b", id: "old"))) else { return fail("an append to a row the rebuild let go asks to resync") }

        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [SessionStreamProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://stream.test")!, token: "t", session: URLSession(configuration: configuration))
        SessionStreamProtocol.serve([
            event(whole(10, inline)) + event(append(11, from: 16384, "é")) + event(append(12, from: 3, "lost")) + event(append(13, from: 16386, "never")) + "hold",
            event(append(20, from: 16386, "stale")) + event(whole(21, inline)) + event(append(22, from: 16384, "é!")),
        ])
        let first = ReceivedRows()
        let started = Date()
        do {
            try await api.streamSession(id: "S", after: 9, onConnected: {}, onEvent: { await first.add($0) })
            fail("a diverged append ends the stream")
        } catch AgenthailAPIError.streamClosed {
        } catch {
            fail("a diverged append ends the stream as closed, so the caller reconnects, got \(error)")
        }
        check(Date().timeIntervalSince(started) < 5, "a diverged append ends the stream without waiting for the server to close it")
        let firstRows = await first.rows
        check(firstRows.map(\.seq) == [10, 11] && firstRows.last?.data.body == inline + "é", "rows before the divergence arrive rebuilt and nothing after it does: \(firstRows.map(\.seq))")

        let resumed = ReceivedRows()
        try? await api.streamSession(id: "S", after: 11, onConnected: {}, onEvent: { await resumed.add($0) })
        let resumedRows = await resumed.rows
        check(SessionStreamProtocol.requests.last?.contains("after=11") == true, "the stream reopens after the last row it applied")
        check(resumedRows.isEmpty, "a new connection keeps no rows from the last one, so an append before its whole row asks to resync: \(resumedRows.map(\.seq))")

        SessionStreamProtocol.serve([event(whole(21, inline)) + event(append(22, from: 16384, "é!"))])
        let fresh = ReceivedRows()
        try? await api.streamSession(id: "S", after: 20, onConnected: {}, onEvent: { await fresh.add($0) })
        let freshRows = await fresh.rows
        check(freshRows.map(\.seq) == [21, 22] && freshRows.last?.data.body == inline + "é!", "after a resume the whole row seen first takes appends: \(freshRows.map(\.seq))")
        print("session stream rebuild tests passed")
    }

    static func fail(_ message: String) {
        print("FAIL: \(message)")
        exit(1)
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fail(message) }
    }
}
