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
        let deadline = Date().addingTimeInterval(timeout)
        var frame = self.frame
        repeat {
            RunLoop.current.run(until: Date().addingTimeInterval(0.3))
            let next = self.frame
            if next == frame {
                tap()
                return
            }
            frame = next
        } while Date() < deadline
        XCTFail("\(self) kept moving", file: file, line: line)
    }
}
