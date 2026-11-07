import XCTest

@MainActor
extension XCUIElement {
    func tapWhenReady(timeout: TimeInterval = 5, file: StaticString = #filePath, line: UInt = #line) {
        guard waitForExistence(timeout: timeout) else {
            XCTFail("\(self) did not appear", file: file, line: line)
            return
        }
        tap()
    }

    /// Taps once the element has stopped moving. A tap while a swipe is still decelerating only stops the scroll.
    func tapWhenStill(timeout: TimeInterval = 5, file: StaticString = #filePath, line: UInt = #line) {
        guard waitUntilStill(timeout: timeout) else {
            XCTFail("\(self) kept moving", file: file, line: line)
            return
        }
        tap()
    }

    func waitUntilStill(timeout: TimeInterval = 10) -> Bool {
        waitUntilSteady(timeout: timeout) { $0.exists ? $0.frame : nil }
    }

    func waitUntilRowsStill(timeout: TimeInterval = 10) -> Bool {
        waitUntilSteady(timeout: timeout) { $0.middleRow() }
    }

    private func middleRow() -> String? {
        guard let root = try? snapshot() else { return nil }
        let middle = root.frame.midY
        var best: XCUIElementSnapshot?
        var pending = [root]
        while let next = pending.popLast() {
            if next.elementType == .staticText, !next.label.isEmpty, root.frame.contains(next.frame),
               abs(next.frame.midY - middle) < abs((best?.frame.midY ?? .infinity) - middle) { best = next }
            pending += next.children
        }
        return best.map { "\($0.label)|\($0.frame)" }
    }

    private func waitUntilSteady<Value: Equatable>(timeout: TimeInterval, _ read: (XCUIElement) -> Value?) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        var last = read(self)
        repeat {
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
            let next = read(self)
            if next != nil, next == last { return true }
            last = next
        } while Date() < deadline
        return false
    }

    // A row under the pinned status bar or the composer still reads as hittable.
    func scrollIntoMiddle(of scrollView: XCUIElement, attempts: Int = 6) {
        var last = CGFloat.infinity
        for _ in 0..<attempts {
            let band = scrollView.frame.insetBy(dx: 0, dy: scrollView.frame.height * 0.25)
            let middle = frame.midY
            if (band.minY...band.maxY).contains(middle) || abs(middle - last) < 1 { return }
            last = middle
            let distance = max(-250, min(250, band.midY - middle))
            let start = scrollView.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            let end = start.withOffset(CGVector(dx: 0, dy: distance))
            start.press(forDuration: 0.05, thenDragTo: end, withVelocity: .slow, thenHoldForDuration: 0.3)
        }
    }
}
