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

        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after opening")
        let before = try luminance(XCUIScreen.main.screenshot().image, in: band)
        let row = app.staticTexts["What the checks show"]
        XCTAssertTrue(row.exists)
        let rowY = row.frame.minY
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after the swipe")
        XCTAssertGreaterThan(abs(row.frame.minY - rowY), 40, "the transcript did not scroll")
        let after = try luminance(XCUIScreen.main.screenshot().image, in: band)

        // The backing's material may retint as content passes; glyph edges change far more.
        let worst = zip(before, after).map { abs($0 - $1) }.max() ?? 0
        XCTAssertLessThan(worst, 0.15, "transcript text shows through the header: a pixel changed by \(worst)")
    }

    func testSolidHeaderIsTheTintedTranscriptPane() throws {
        let app = XCUIApplication()
        // A light pane keeps a tint only when every text role clears the contrast target on it.
        app.launchArguments = ["--preview-session", "--preview-theme", "light", "--preview-backdrop",
                               "--preview-value", "glass.tint=#a0c0f0", "--preview-value", "contrast.target=1.5"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let working = app.staticTexts["Working"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after opening")
        let width = app.windows.firstMatch.frame.width
        let header = CGRect(x: 2, y: working.frame.maxY + 2, width: width - 4, height: 4)
        let pane = CGRect(x: 2, y: working.frame.maxY + 40, width: 8, height: 40)
        let shot = XCUIScreen.main.screenshot().image
        let paneColor = try flatColor(shot, in: pane)
        XCTAssertGreaterThan(paneColor[2] - paneColor[0], 0.05, "the tint \(paneColor) never reached the transcript pane")
        let headerColor = try flatColor(shot, in: header)
        let worst = zip(headerColor, paneColor).map { abs($0 - $1) }.max() ?? 1
        XCTAssertLessThan(worst, 0.02, "the header \(headerColor) differs from the transcript pane \(paneColor)")
    }

    private func flatColor(_ image: UIImage, in rect: CGRect) throws -> [Float] {
        let pixels = try rgb(image, in: rect)
        let mean = (0..<3).map { c in pixels.map { $0[c] }.reduce(0, +) / Float(max(pixels.count, 1)) }
        let spread = pixels.flatMap { pixel in (0..<3).map { abs(pixel[$0] - mean[$0]) } }.max() ?? 0
        XCTAssertLessThan(spread, 0.03, "the area \(rect) is not one flat colour around \(mean)")
        return mean
    }

    private func luminance(_ image: UIImage, in rects: [CGRect]) throws -> [Float] {
        try rects.flatMap { try rgb(image, in: $0) }.map { 0.2126 * $0[0] + 0.7152 * $0[1] + 0.0722 * $0[2] }
    }

    private func rgb(_ image: UIImage, in rect: CGRect) throws -> [[Float]] {
        let cg = try XCTUnwrap(image.cgImage)
        let scale = CGFloat(cg.width) / image.size.width
        let crop = try XCTUnwrap(cg.cropping(to: CGRect(x: rect.minX * scale, y: rect.minY * scale, width: rect.width * scale, height: rect.height * scale)))
        let w = crop.width, h = crop.height
        var bytes = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = try XCTUnwrap(CGContext(data: &bytes, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                          space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(crop, in: CGRect(x: 0, y: 0, width: w, height: h))
        return (0..<(w * h)).map { i in (0..<3).map { Float(bytes[i * 4 + $0]) / 255 } }
    }
}
