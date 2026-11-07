import XCTest

@MainActor
extension XCUIApplication {
    var jumpToLatest: XCUIElement { buttons["Jump to latest"] }

    // Longer than the one second Jump to latest waits before it shows for a follow that is stuck behind.
    func waitForSteadyJump(timeout: TimeInterval = 10) {
        let deadline = Date().addingTimeInterval(timeout)
        var state = jumpToLatest.exists
        var since = Date()
        while Date() < deadline, Date().timeIntervalSince(since) < 1.5 {
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            let next = jumpToLatest.exists
            if next != state {
                state = next
                since = Date()
            }
        }
    }

    func assertJumpHoldsStill(_ context: String, file: StaticString = #filePath, line: UInt = #line) {
        let first = jumpToLatest.exists
        for _ in 0..<8 {
            RunLoop.current.run(until: Date().addingTimeInterval(0.15))
            XCTAssertEqual(jumpToLatest.exists, first, "Jump to latest kept appearing and disappearing \(context)", file: file, line: line)
        }
    }
}
