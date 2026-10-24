import SwiftUI
import UIKit
import XCTest
@testable import Agenthail

@MainActor
final class UsageMetersLineTests: XCTestCase {
    private let now = Date(timeIntervalSince1970: 1_790_000_000)

    private func meter(_ label: String, _ percent: Double, hours: Double, critical: Bool = false) -> UsageReport.Meter {
        let until = ISO8601DateFormatter().string(from: now.addingTimeInterval(hours * 3600))
        return UsageReport.Meter(provider: "claude", kind: "session", label: label, percent: percent, severity: critical ? "critical" : nil, resetsAt: until, asOf: ISO8601DateFormatter().string(from: now), aged: false, until: until)
    }

    private var strip: UsageStrip {
        UsageStrip(
            meters: [meter("Claude", 40, hours: 2.2), meter("Claude week", 62, hours: 76), meter("Codex week", 19, hours: 120)],
            others: [
                .init(id: "a", title: "team", percent: 91, critical: true, spoken: "team@example.com: Claude week usage 91 percent"),
                .init(id: "b", title: "ops · build-box", percent: 12, critical: false, spoken: "ops@example.com on build-box: Claude usage 12 percent"),
            ]
        )
    }

    private func width(at available: CGFloat) -> CGFloat {
        let host = UIHostingController(rootView: UsageMetersLine(strip: strip, now: now))
        return ceil(host.sizeThatFits(in: CGSize(width: available, height: .greatestFiniteMagnitude)).width)
    }

    func testTheLineNeverOverflowsAPhoneWidth() {
        for available in [320.0, 375, 393, 430] as [CGFloat] {
            XCTAssertLessThanOrEqual(width(at: available), available, "at \(available)pt the usage line must fall back rather than overflow")
        }
    }

    func testAWideRowShowsMoreThanANarrowOne() {
        XCTAssertGreaterThan(width(at: 2000), width(at: 320), "with room the line shows reset times and other accounts")
    }

    func testEveryItemReadsOutItsFullText() {
        let other = strip.others[0]
        XCTAssertEqual(UsageStrip.make([], now: now), UsageStrip())
        XCTAssertTrue(other.spoken.contains("91 percent"))
        XCTAssertTrue(UsageMeterText.spokenLabel(strip.meters[0], now: now).hasPrefix("Claude usage 40 percent"))
    }
}
