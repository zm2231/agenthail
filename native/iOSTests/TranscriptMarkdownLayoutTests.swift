import SwiftUI
import XCTest
@testable import Agenthail

@MainActor
final class TranscriptMarkdownLayoutTests: XCTestCase {
    private static let markdown = """
    # Reconnect is fixed

    The pending build survives reconnecting. **All 8 regression tests pass**, and `restore()` keeps the identity.

    ## What changed

    - Preserved the session identity
      - Kept the queued instruction attached
        1. Saved before the drop
        2. Replayed after

    > The queue keeps its order across reconnects.
    >
    > - Even when two hosts answer

    ```swift
    await session.resume()
    ```

    | Check | Result |
    | --- | --- |
    | Reconnect | Passed |

    ---

    The latest update is visible above the composer.
    """

    func testEveryTranscriptMarkdownStyleLaysOutAtItsFinalSizeFirst() async throws {
        for reading in [false, true] {
            for textStyle in [Font.TextStyle.body, .subheadline] {
                let host = UIHostingController(rootView: SessionMarkdown(text: Self.markdown, readingStyle: reading, textStyle: textStyle).frame(width: 360))
                let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 360, height: 3000))
                window.rootViewController = host
                window.isHidden = false
                let fitting = CGSize(width: 360, height: CGFloat.greatestFiniteMagnitude)
                let first = host.sizeThatFits(in: fitting)
                for _ in 0..<10 {
                    host.view.setNeedsLayout()
                    host.view.layoutIfNeeded()
                    try await Task.sleep(for: .milliseconds(30))
                }
                let settled = host.sizeThatFits(in: fitting)
                window.isHidden = true
                XCTAssertGreaterThan(settled.height, 300, "reading \(reading), \(textStyle)")
                XCTAssertEqual(first, settled, "a transcript row grows after its first layout, reading \(reading), \(textStyle)")
            }
        }
    }
}
