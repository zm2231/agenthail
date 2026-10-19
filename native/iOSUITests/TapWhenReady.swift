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
}
