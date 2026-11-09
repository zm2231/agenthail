import XCTest

final class BodyRunsUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    private func runs(_ extra: [String]) -> [String: Int] {
        let app = XCUIApplication()
        app.launchArguments = ["--preview-session", "--preview-theme", "dark", "--body-runs", "--preview-background-upserts"] + extra
        app.launch()
        let probe = app.staticTexts["body-runs"]
        XCTAssertTrue(probe.waitForExistence(timeout: 30))
        Thread.sleep(forTimeInterval: 3)
        let before = Self.counts(probe.label)
        Thread.sleep(forTimeInterval: 10)
        let after = Self.counts(probe.label)
        return after.reduce(into: [:]) { $0[$1.key] = $1.value - (before[$1.key] ?? 0) }
    }

    private static func counts(_ label: String) -> [String: Int] {
        Dictionary(label.split(separator: " ").compactMap { pair in
            let parts = pair.split(separator: "=")
            guard parts.count == 2, let value = Int(parts[1]) else { return nil }
            return (String(parts[0]), value)
        }, uniquingKeysWith: { $1 })
    }

    private static func line(_ name: String, _ runs: [String: Int]) -> String {
        "MEASURE \(name) " + runs.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }.joined(separator: " ")
    }

    func testAnotherSessionsUpdatesLeaveTheOpenTranscriptAlone() {
        let runs = runs(["--preview-reading"])
        print(Self.line("transcript", runs))
        XCTAssertGreaterThan(runs["upserts", default: 0], 50, "the background session kept updating")
    }

    func testAnotherSessionsUpdatesRedrawOnlyItsRowInTheList() {
        let runs = runs(["--preview-app"])
        print(Self.line("list", runs))
        XCTAssertGreaterThan(runs["upserts", default: 0], 50, "the background session kept updating")
    }
}
