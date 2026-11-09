import Foundation
import XCTest

final class FixtureScopeTests: XCTestCase {
    func testOneTestKeepsOneScopeForEveryModelItMakes() {
        XCTAssertTrue(FixtureScope.open(self) === FixtureScope.open(self))
    }

    func testARequestFromAnEarlierTestIsRefusedAndNotCounted() async throws {
        ScopeProbe.state.reset()
        let earlier = FixtureScope.open(self).session([ScopeProbe.self])
        _ = try await earlier.data(from: URL(string: "https://probe.invalid/count")!)
        XCTAssertEqual(ScopeProbe.state.counted, 1)

        let nextTest = NSObject()
        let next = FixtureScope.begin(owner: ObjectIdentifier(nextTest)).scope
        defer { next.retire(); withExtendedLifetime(nextTest) {} }
        ScopeProbe.state.reset()
        await XCTAssertThrowsCancelled { _ = try await earlier.data(from: URL(string: "https://probe.invalid/count")!) }
        _ = try await next.session([ScopeProbe.self]).data(from: URL(string: "https://probe.invalid/count")!)
        XCTAssertEqual(ScopeProbe.state.counted, 1, "only the current test's request is counted")
    }

    func testAReplyHeldAcrossATestBoundaryIsRefusedNotAnswered() async throws {
        ScopeProbe.state.reset()
        ScopeProbe.state.hold()
        let earlier = FixtureScope.open(self).session([ScopeProbe.self])
        let held = Task { try await earlier.data(from: URL(string: "https://probe.invalid/held")!) }
        let deadline = Date().addingTimeInterval(2)
        while !ScopeProbe.state.waiting, Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(ScopeProbe.state.waiting)

        let nextTest = NSObject()
        let next = FixtureScope.begin(owner: ObjectIdentifier(nextTest)).scope
        defer { next.retire(); withExtendedLifetime(nextTest) {}; ScopeProbe.state.release() }
        await XCTAssertThrowsCancelled { _ = try await held.value }
        XCTAssertEqual(ScopeProbe.state.answered, 0, "a held reply reached a model from an earlier test")
    }

    func testAChangeAnEarlierRequestIsMakingLandsBeforeTheNextTestBegins() async throws {
        ScopeProbe.state.reset()
        ScopeProbe.state.slow()
        let earlier = FixtureScope.open(self).session([ScopeProbe.self])
        let slow = Task { try? await earlier.data(from: URL(string: "https://probe.invalid/slow")!) }
        let deadline = Date().addingTimeInterval(2)
        while !ScopeProbe.state.started, Date() < deadline { try await Task.sleep(for: .milliseconds(5)) }
        XCTAssertTrue(ScopeProbe.state.started)

        let nextTest = NSObject()
        let next = FixtureScope.begin(owner: ObjectIdentifier(nextTest)).scope
        defer { next.retire(); withExtendedLifetime(nextTest) {} }
        ScopeProbe.state.reset()
        _ = await slow.value
        XCTAssertEqual(ScopeProbe.state.counted, 0, "an earlier request's change landed after the next test reset the fixture")
    }

    func testAStreamKeptFromAnEarlierTestGetsNothingSentAfterTheTestEnds() async throws {
        ScopeProbe.state.reset()
        let earlier = FixtureScope.open(self).session([ScopeProbe.self])
        let (bytes, _) = try await earlier.bytes(from: URL(string: "https://probe.invalid/stream")!)
        let lines = Task { () -> (lines: [String], error: Error?) in
            var lines: [String] = []
            do {
                for try await line in bytes.lines { lines.append(line) }
                return (lines, nil)
            } catch {
                return (lines, error)
            }
        }
        let deadline = Date().addingTimeInterval(2)
        while !ScopeProbe.state.streaming, Date() < deadline { try await Task.sleep(for: .milliseconds(5)) }
        XCTAssertTrue(ScopeProbe.state.streaming)

        let nextTest = NSObject()
        let next = FixtureScope.begin(owner: ObjectIdentifier(nextTest)).scope
        defer { next.retire(); withExtendedLifetime(nextTest) {} }
        ScopeProbe.state.emit("late")
        let read = await lines.value
        XCTAssertFalse(read.lines.contains("late"), "an event reached a stream kept from an earlier test: \(read.lines)")
        XCTAssertEqual((read.error as? URLError)?.code, .cancelled, "\(String(describing: read.error))")
    }

    private func XCTAssertThrowsCancelled(_ body: () async throws -> Void, file: StaticString = #filePath, line: UInt = #line) async {
        do {
            try await body()
            XCTFail("the request was answered", file: file, line: line)
        } catch {
            XCTAssertEqual((error as? URLError)?.code, .cancelled, "\(error)", file: file, line: line)
        }
    }
}

private final class ScopeProbe: URLProtocol, @unchecked Sendable {
    final class State: @unchecked Sendable {
        private let condition = NSCondition()
        private var count = 0
        private var replies = 0
        private var holding = false
        private var holdWaiting = false

        init() {
            let condition = condition
            FixtureScope.wakeOnRetire { condition.withLock { condition.broadcast() } }
        }

        var counted: Int { condition.withLock { count } }
        var answered: Int { condition.withLock { replies } }
        var waiting: Bool { condition.withLock { holdWaiting } }
        private var slowly = false
        private var begun = false
        var started: Bool { condition.withLock { begun } }
        func reset() { condition.withLock { stream = nil; count = 0; replies = 0; holding = false; holdWaiting = false; slowly = false; begun = false; condition.broadcast() } }
        func hold() { condition.withLock { holding = true } }
        private var stream: ScopeProbe?
        var streaming: Bool { condition.withLock { stream != nil } }
        func open(_ request: ScopeProbe) { condition.withLock { stream = request } }
        func emit(_ line: String) {
            guard let stream = condition.withLock({ stream }) else { return }
            stream.served {
                stream.client?.urlProtocol(stream, didLoad: Data("\(line)\n".utf8))
                stream.client?.urlProtocolDidFinishLoading(stream)
            }
        }
        func arrived(_ request: URLRequest) { if request.url?.path == "/count" { condition.withLock { count += 1 } } }
        func slow() { condition.withLock { slowly = true } }
        func release() { condition.withLock { holding = false; condition.broadcast() } }

        func answer(_ request: URLRequest) -> Bool {
            if condition.withLock({ begun = true; return slowly }) {
                Thread.sleep(forTimeInterval: 0.3)
                condition.withLock { count += 1 }
                return false
            }
            condition.lock(); defer { condition.unlock() }
            guard FixtureScope.owns(request) else { return false }
            if holding {
                holdWaiting = true
                while holding, FixtureScope.owns(request) { condition.wait() }
                holdWaiting = false
            }
            guard FixtureScope.owns(request) else { return false }
            replies += 1
            return true
        }
    }

    static let state = State()
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        served {
            Self.state.arrived(request)
            if request.url?.path == "/stream" {
                client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data("open\n".utf8))
                return Self.state.open(self)
            }
            later { [self] in
                guard Self.state.answer(request) else { return refuse() }
                client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data("{}".utf8))
                client?.urlProtocolDidFinishLoading(self)
            }
        }
    }
}
