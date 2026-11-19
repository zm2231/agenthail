import XCTest

@MainActor
final class SessionHeaderUITests: XCTestCase {
    private func launch(_ size: String, theme: String = "dark") -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", theme, "--preview-reading", "--preview-reading-top", "-UIPreferredContentSizeCategoryName", size]
        app.launch()
        XCTAssertTrue(app.buttons["session-summary"].waitForExistence(timeout: 30), "no session header")
        sleep(2)
        return app
    }

    private func keep(_ app: XCUIApplication, _ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    private static func pixels(_ screenshot: XCUIScreenshot, _ rect: CGRect, scale: CGFloat) -> [UInt8] {
        guard let image = UIImage(data: screenshot.pngRepresentation)?.cgImage,
              let cropped = image.cropping(to: CGRect(x: rect.minX * scale, y: rect.minY * scale, width: rect.width * scale, height: rect.height * scale)) else { return [] }
        var bytes = [UInt8](repeating: 0, count: cropped.width * cropped.height * 4)
        let context = CGContext(data: &bytes, width: cropped.width, height: cropped.height, bitsPerComponent: 8, bytesPerRow: cropped.width * 4,
                                space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        context?.draw(cropped, in: CGRect(x: 0, y: 0, width: cropped.width, height: cropped.height))
        return bytes
    }

    private static func difference(_ a: [UInt8], _ b: [UInt8]) -> Double {
        guard a.count == b.count, !a.isEmpty else { return .infinity }
        let total = zip(a, b).reduce(0) { $0 + abs(Int($1.0) - Int($1.1)) }
        return Double(total) / Double(a.count)
    }

    private func scrolledTextStaysOutOfTheTitle(_ size: String, name: String) -> Double {
        let app = launch(size)
        let window = app.windows.firstMatch.frame
        let summary = app.buttons["session-summary"].frame
        let band = CGRect(x: 0, y: 64, width: window.width, height: max(1, summary.minY - 64))
        let scale = UIScreen.main.scale
        let before = Self.pixels(app.screenshot(), band, scale: scale)
        app.swipeUp(velocity: .slow)
        sleep(2)
        let after = Self.pixels(app.screenshot(), band, scale: scale)
        let changed = Self.difference(before, after)
        print("MEASURE header \(name) titleBandChange=\(changed) summaryMaxY=\(summary.maxY) share=\(summary.maxY / window.height)")
        keep(app, "Header mid-scroll, \(name)")
        return changed
    }

    // Measured on the shared simulator: 0.2 to 0.9 with the header's own ground, 6 to 13 when only the scroll edge blurs what is under it.
    private static let titleBandLimit = 2.0

    func testScrolledTranscriptTextNeverShowsUnderTheHeader() {
        let changes = [("UICTContentSizeCategoryL", "default"), ("UICTContentSizeCategoryAccessibilityXXXL", "ax5")].map { size, name in
            (name, scrolledTextStaysOutOfTheTitle(size, name: name))
        }
        for (name, changed) in changes {
            XCTAssertLessThan(changed, Self.titleBandLimit, "the title band changed by \(changed) per channel as the transcript scrolled under it at \(name)")
        }
    }

    func testARestoredDraftShowsWithoutThePlaceholder() {
        let app = launch("UICTContentSizeCategoryL")
        let input = app.textViews["composer-input"]
        XCTAssertTrue(input.waitForExistence(timeout: 10))
        XCTAssertEqual(input.value as? String, "Keep the regression check with the fix.")
        XCTAssertFalse(app.staticTexts["Message this agent…"].exists, "the placeholder is in the accessibility tree beside a draft")
        keep(app, "Composer with a restored draft")
    }

    func testTheStatusRowStaysOneLineAndTheHeaderSmallAtTheLargestTextSize() {
        let app = launch("UICTContentSizeCategoryAccessibilityXXXL")
        let summary = app.buttons["session-summary"].frame
        let window = app.windows.firstMatch.frame
        XCTAssertLessThanOrEqual(summary.maxY / window.height, 0.2, "the header ends at \(summary.maxY) of \(window.height)")
        XCTAssertLessThanOrEqual(summary.height, 60, "the status row wrapped: \(summary)")
        keep(app, "Header at the largest text size")
    }
}
