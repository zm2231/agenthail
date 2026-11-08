import XCTest

@MainActor
final class TranscriptScrollDeltaUITests: XCTestCase {
    private struct Drag { let direction: String; let label: String; let moved: CGFloat? }

    func testUniformRepliesMoveOnlyWithTheFinger() throws {
        try measure(["--preview-stream-paused"], name: "uniform")
    }

    func testVariedRepliesMoveOnlyWithTheFinger() throws {
        try measure(["--preview-stream-paused", "--preview-stream-varied"], name: "varied")
    }

    private func midScreenText(_ timeline: XCUIElement) throws -> String? {
        let band = timeline.frame.insetBy(dx: 0, dy: timeline.frame.height * 0.3)
        var pending = [try timeline.snapshot()]
        var seen: [String: Int] = [:]
        var best: String?
        while let next = pending.popLast() {
            if next.elementType == .staticText, next.label.count > 12 {
                let key = String(next.label.prefix(48))
                seen[key, default: 0] += 1
                if band.contains(CGPoint(x: next.frame.midX, y: next.frame.midY)) { best = key }
            }
            pending += next.children
        }
        return best.flatMap { seen[$0] == 1 ? $0 : nil }
    }

    private func drag(_ timeline: XCUIElement, from: CGFloat, to: CGFloat, direction: String) throws -> Drag {
        guard let label = try midScreenText(timeline) else { return Drag(direction: direction, label: "-", moved: nil) }
        let row = timeline.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", label)).firstMatch
        let before = row.frame.minY
        timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: from))
            .press(forDuration: 0.05, thenDragTo: timeline.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: to)), withVelocity: 300, thenHoldForDuration: 0.4)
        RunLoop.current.run(until: Date().addingTimeInterval(0.4))
        return Drag(direction: direction, label: String(label.prefix(40)), moved: row.exists ? row.frame.minY - before : nil)
    }

    private func measure(_ extra: [String], name: String) throws {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-stream", "--preview-theme", "dark", "--scroll-probe"] + extra
        app.launch()
        let timeline = app.scrollViews["session-timeline"]
        XCTAssertTrue(timeline.waitForExistence(timeout: 15))
        XCTAssertTrue(timeline.staticTexts.matching(NSPredicate(format: "label CONTAINS 'of reply '")).firstMatch.waitForExistence(timeout: 15), "the transcript showed no reply")
        XCTAssertTrue(timeline.waitUntilRowsStill(), "the transcript kept moving after it opened")

        var drags: [Drag] = []
        for _ in 0..<18 { drags.append(try drag(timeline, from: 0.3, to: 0.6, direction: "up")) }
        for _ in 0..<6 { timeline.swipeDown() }
        _ = timeline.waitUntilRowsStill()
        for _ in 0..<18 { drags.append(try drag(timeline, from: 0.6, to: 0.3, direction: "down")) }
        for _ in 0..<6 { timeline.swipeUp() }
        _ = timeline.waitUntilRowsStill()

        let probe = app.staticTexts["scroll-jumps"].label
        let log = app.staticTexts["scroll-jump-log"].exists ? app.staticTexts["scroll-jump-log"].label : ""
        var report = ["\(name): probe \(probe)", "log: \(log)"]
        var offCount = 0
        for direction in ["up", "down"] {
            let moved = drags.filter { $0.direction == direction }.compactMap(\.moved).sorted()
            guard !moved.isEmpty else { report.append("\(direction): no tracked rows"); continue }
            let median = moved[moved.count / 2]
            let off = drags.filter { $0.direction == direction }.compactMap { drag in drag.moved.map { (drag.label, $0 - median) } }.filter { abs($0.1) > 8 }
            report.append(String(format: "%@: %d drags tracked, median %.1f pt, min %.1f, max %.1f, off by >8 pt: %d", direction, moved.count, median, moved.first!, moved.last!, off.count))
            offCount += off.count
            report += off.map { String(format: "  %@ off %.1f", $0.0, $0.1) }
        }
        let text = report.joined(separator: "\n")
        print("SCROLL-DELTA\n\(text)")
        let attachment = XCTAttachment(string: text)
        attachment.name = "\(name) scroll deltas"
        attachment.lifetime = .keepAlways
        add(attachment)
        let jumps = TranscriptScrollJumpUITests.parse(probe).jumps
        XCTAssertEqual(jumps, 0, text)
        XCTAssertEqual(offCount, 0, text)
    }
}
