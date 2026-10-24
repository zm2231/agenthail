import XCTest

@MainActor
final class SessionHeaderBackingUITests: XCTestCase {
    func testTranscriptScrollingUnderTheHeaderDoesNotShowThroughIt() throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-theme", "dark"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let working = app.staticTexts["Working"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        let bar = app.navigationBars.firstMatch.frame
        let menu = app.buttons["Session menu"].frame.insetBy(dx: -4, dy: -4)
        let bottom = working.frame.maxY + 8
        // The menu is glass, which refracts whatever passes under it.
        let band = [
            CGRect(x: 0, y: bar.minY, width: menu.minX, height: bottom - bar.minY),
            CGRect(x: menu.minX, y: menu.maxY, width: bar.width - menu.minX, height: bottom - menu.maxY),
        ]
        XCTAssertLessThan(menu.maxY, working.frame.minY)

        sleep(2)
        let before = try pixels(XCUIScreen.main.screenshot().image, in: band)
        let row = app.staticTexts["What the checks show"]
        XCTAssertTrue(row.exists)
        let rowY = row.frame.minY
        timeline.swipeDown(velocity: .slow)
        sleep(2)
        XCTAssertGreaterThan(abs(row.frame.minY - rowY), 40, "the transcript did not scroll")
        let after = try pixels(XCUIScreen.main.screenshot().image, in: band)

        // The backing's material may retint as content passes; glyph edges change far more.
        let worst = zip(before, after).map { abs($0 - $1) }.max() ?? 0
        XCTAssertLessThan(worst, 0.15, "transcript text shows through the header: a pixel changed by \(worst)")
    }

    private func pixels(_ image: UIImage, in rects: [CGRect]) throws -> [Float] {
        try rects.flatMap { try pixels(image, in: $0) }
    }

    private func pixels(_ image: UIImage, in rect: CGRect) throws -> [Float] {
        let cg = try XCTUnwrap(image.cgImage)
        let scale = CGFloat(cg.width) / image.size.width
        let crop = try XCTUnwrap(cg.cropping(to: CGRect(x: rect.minX * scale, y: rect.minY * scale, width: rect.width * scale, height: rect.height * scale)))
        let w = crop.width, h = crop.height
        var bytes = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = try XCTUnwrap(CGContext(data: &bytes, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                          space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(crop, in: CGRect(x: 0, y: 0, width: w, height: h))
        return (0..<(w * h)).map { i in
            (0.2126 * Float(bytes[i * 4]) + 0.7152 * Float(bytes[i * 4 + 1]) + 0.0722 * Float(bytes[i * 4 + 2])) / 255
        }
    }
}
