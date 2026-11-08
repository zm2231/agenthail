import XCTest

@MainActor
final class TranscriptScrimCoverageUITests: XCTestCase {
    private static let bright = "mesh: 3x3, #e8a33d, #4fa3c7, #d95f76, #7fc8a9, #f2d16b, #6b8fd9, #c46bd9, #59c7c7, #e88d5a"

    func testEveryLineSitsOnScrimInDarkPortrait() throws { try check(dark: true, landscape: false) }
    func testEveryLineSitsOnScrimInLightPortrait() throws { try check(dark: false, landscape: false) }
    func testEveryLineSitsOnScrimInDarkLandscape() throws { try check(dark: true, landscape: true) }
    func testEveryLineSitsOnScrimInLightLandscape() throws { try check(dark: false, landscape: true) }

    private func check(dark: Bool, landscape: Bool) throws {
        if landscape {
            XCUIDevice.shared.orientation = .landscapeLeft
            addTeardownBlock { @MainActor in XCUIDevice.shared.orientation = .portrait }
        }
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-theme", dark ? "dark" : "light", "--preview-backdrop", Self.bright]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after opening")
        let name = "\(dark ? "dark" : "light") \(landscape ? "landscape" : "portrait")"
        var report = [name]
        var bad = 0
        for pass in 0..<3 {
            if pass > 0 {
                timeline.swipeDown(velocity: .slow)
                _ = timeline.waitUntilRowsStill()
            }
            let shot = XCUIScreen.main.screenshot()
            let image = shot.image
            let view = timeline.frame
            let top = app.navigationBars.firstMatch.frame.maxY + 60
            let art = try chroma(image, in: CGRect(x: 1, y: view.midY - 40, width: 3, height: 80))
            report.append(String(format: "pass %d: art chroma beside the column %.3f, shot %@ window %@", pass, art, "\(image.size)", "\(app.windows.firstMatch.frame.size)"))
            var pending = [try timeline.snapshot()]
            while let next = pending.popLast() {
                pending += next.children
                let frame = next.frame.insetBy(dx: 1, dy: 1)
                guard next.elementType == .staticText, frame.width > 24, frame.height > 8,
                      frame.minY > top, frame.maxY < view.maxY - 120, frame.minX >= 0, frame.maxX <= view.maxX else { continue }
                let under = try chroma(image, in: frame)
                let covered = under < max(0.06, art * 0.5)
                if !covered { bad += 1 }
                if !covered || report.count < 12 {
                    report.append(String(format: "  %@ %.3f at %@ '%@'", covered ? "ok " : "BARE", under, "\(frame.integral)", String(next.label.prefix(40))))
                }
            }
            let attachment = XCTAttachment(screenshot: shot)
            attachment.name = "\(name) pass \(pass)"
            attachment.lifetime = .keepAlways
            add(attachment)
        }
        let text = report.joined(separator: "\n")
        print("SCRIM-COVERAGE\n\(text)")
        let attachment = XCTAttachment(string: text)
        attachment.name = "\(name) coverage"
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertEqual(bad, 0, text)
    }

    func testTranscriptScrollingUnderTheHeaderDoesNotShowThroughItInLight() throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-reading", "--preview-theme", "light"]
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 10))
        let working = app.staticTexts["Working"]
        XCTAssertTrue(working.waitForExistence(timeout: 10))
        let bar = app.navigationBars.firstMatch.frame
        let menu = app.buttons["Session menu"].frame.insetBy(dx: -4, dy: -4)
        let band = CGRect(x: 0, y: bar.minY, width: menu.minX, height: working.frame.maxY + 8 - bar.minY)
        XCTAssertTrue(timeline.waitUntilRowsStill())
        let before = try pixels(XCUIScreen.main.screenshot().image, in: band)
        timeline.swipeDown(velocity: .slow)
        XCTAssertTrue(timeline.waitUntilRowsStill())
        let after = try pixels(XCUIScreen.main.screenshot().image, in: band)
        let worst = zip(before, after).map { abs(luma($0) - luma($1)) }.max() ?? 0
        print(String(format: "SCRIM-COVERAGE light header worst change %.3f", worst))
        XCTAssertLessThan(worst, 0.15, "transcript text shows through the header in light: a pixel changed by \(worst)")
    }

    private func luma(_ p: [Float]) -> Float { 0.2126 * p[0] + 0.7152 * p[1] + 0.0722 * p[2] }

    private func chroma(_ image: UIImage, in rect: CGRect) throws -> Float {
        let values = try pixels(image, in: rect).map { ($0.max() ?? 0) - ($0.min() ?? 0) }.sorted()
        return values.isEmpty ? 0 : values[values.count / 2]
    }

    private func pixels(_ image: UIImage, in rect: CGRect) throws -> [[Float]] {
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
