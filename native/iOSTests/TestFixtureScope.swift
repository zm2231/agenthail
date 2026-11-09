import Foundation
import XCTest

final class FixtureScope: @unchecked Sendable {
    static let header = "X-Fixture-Scope"
    private static let condition = NSCondition()
    nonisolated(unsafe) private static var current: (owner: ObjectIdentifier, scope: FixtureScope)?
    nonisolated(unsafe) private static var wakers: [@Sendable () -> Void] = []

    let tag = UUID().uuidString
    private var running = 0

    static func open(_ test: XCTestCase) -> FixtureScope {
        let (scope, opened) = begin(owner: ObjectIdentifier(test))
        if opened {
            test.addTeardownBlock {
                XCTAssertTrue(scope.retire(), "a fixture was still answering a request when the test ended")
            }
        }
        return scope
    }

    static func begin(owner: ObjectIdentifier) -> (scope: FixtureScope, opened: Bool) {
        let (scope, opened, replaced) = condition.withLock { () -> (FixtureScope, Bool, FixtureScope?) in
            if let current, current.owner == owner { return (current.scope, false, nil) }
            let scope = FixtureScope()
            let replaced = current?.scope
            current = (owner, scope)
            return (scope, true, replaced)
        }
        if let replaced { replaced.drain() }
        return (scope, opened)
    }

    func configuration(_ protocols: [AnyClass]) -> URLSessionConfiguration {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = protocols
        configuration.httpAdditionalHeaders = [Self.header: tag]
        return configuration
    }

    func session(_ protocols: [AnyClass]) -> URLSession {
        URLSession(configuration: configuration(protocols))
    }

    @discardableResult
    func retire() -> Bool {
        Self.condition.withLock {
            if Self.current?.scope === self { Self.current = nil }
        }
        return drain()
    }

    @discardableResult
    private func drain() -> Bool {
        for wake in Self.condition.withLock({ Self.wakers }) { wake() }
        let deadline = Date().addingTimeInterval(10)
        return Self.condition.withLock {
            while running > 0, Self.condition.wait(until: deadline) {}
            return running == 0
        }
    }

    static func owns(_ request: URLRequest?) -> Bool {
        let tag = request?.value(forHTTPHeaderField: header)
        return condition.withLock { tag != nil && tag == current?.scope.tag }
    }

    static func wakeOnRetire(_ wake: @escaping @Sendable () -> Void) {
        condition.withLock { wakers.append(wake) }
    }

    fileprivate static func enter(_ request: URLRequest) -> FixtureScope? {
        let tag = request.value(forHTTPHeaderField: header)
        return condition.withLock {
            guard let scope = current?.scope, tag == scope.tag else { return nil }
            scope.running += 1
            return scope
        }
    }

    fileprivate func leave() {
        Self.condition.withLock {
            running -= 1
            Self.condition.broadcast()
        }
    }
}

extension URLProtocol {
    func served(_ body: () -> Void) {
        guard let scope = FixtureScope.enter(request) else { return refuse() }
        defer { scope.leave() }
        body()
    }

    func later(_ delay: TimeInterval = 0, _ body: @escaping () -> Void) {
        nonisolated(unsafe) let fixture = self
        nonisolated(unsafe) let body = body
        DispatchQueue.global().asyncAfter(deadline: .now() + delay) { fixture.served(body) }
    }

    func refuse() {
        client?.urlProtocol(self, didFailWithError: URLError(.cancelled))
    }
}
