import XCTest

@MainActor
final class QueueRowOpenUnderLoadUITests: XCTestCase {
    private static let runs = 15
    private static let spin: TimeInterval = 10

    private static func hostLoad() -> Double {
        var load = [Double](repeating: 0, count: 3)
        return getloadavg(&load, 3) > 0 ? load[0] : .infinity
    }

    private static func spinHalfTheCores(for seconds: TimeInterval) {
        let end = Date().addingTimeInterval(seconds)
        for _ in 0..<max(1, ProcessInfo.processInfo.activeProcessorCount / 2) {
            Thread.detachNewThread {
                var x = 0.0
                while Date() < end { x = (x + 1).squareRoot() }
                _ = x
            }
        }
    }

    func testTheLastQueuedRowIsNotCoveredWhenOpenedUnderLoad() throws {
        let gate = Date().addingTimeInterval(900)
        while Self.hostLoad() >= 15, Date() < gate { Thread.sleep(forTimeInterval: 10) }
        guard Self.hostLoad() < 15 else { throw XCTSkip("host load stayed at \(Self.hostLoad()) for 15 minutes") }

        var lines = ["run\tload\tgap\tgapLater\tjump\tprobe"]
        var covered = 0
        for run in 1...Self.runs {
            let load = Self.hostLoad()
            let started = Date()
            Self.spinHalfTheCores(for: Self.spin)
            let app = XCUIApplication()
            app.launchArguments = ["--preview-session", "--preview-tail", "-preview-tail", "one", "--preview-theme", "dark", "--preview-reading", "--preview-follow", "--scroll-probe"]
            app.launch()
            let composer = app.textViews["composer-input"]
            let row = app.descendants(matching: .any)["queue-computer/21"]
            XCTAssertTrue(composer.waitForExistence(timeout: 30), "run \(run): no composer")
            XCTAssertTrue(row.waitForExistence(timeout: 30), "run \(run): no queued row")
            _ = row.waitUntilStill(timeout: 15)
            let gap = composer.frame.minY - row.frame.maxY
            let probe = app.staticTexts["scroll-end"]
            let first = probe.exists ? probe.label : "-"
            RunLoop.current.run(until: Date().addingTimeInterval(1.5))
            let jump = app.buttons["Jump to latest"].exists
            let later = composer.frame.minY - row.frame.maxY
            let second = probe.exists ? probe.label : "-"
            if gap < 0 || later < 0 { covered += 1 }
            lines.append(String(format: "%d\t%.1f\t%.1f\t%.1f\t%@\t%@ | %@", run, load, gap, later, jump ? "yes" : "no", first, second))
            if gap < 0 || later < 0 {
                let shot = XCTAttachment(screenshot: app.screenshot())
                shot.name = "run \(run) covered"
                shot.lifetime = .keepAlways
                add(shot)
            }
            app.terminate()
            let rest = started.addingTimeInterval(Self.spin + 1)
            if rest > Date() { Thread.sleep(until: rest) }
        }
        let table = lines.joined(separator: "\n")
        print("QUEUE-OPEN-LOAD\n\(table)")
        let attachment = XCTAttachment(string: table)
        attachment.name = "queue row open under load"
        attachment.lifetime = .keepAlways
        add(attachment)
        XCTAssertEqual(covered, 0, "the composer covered the last queued row in \(covered) of \(Self.runs) opens:\n\(table)")
    }
}
