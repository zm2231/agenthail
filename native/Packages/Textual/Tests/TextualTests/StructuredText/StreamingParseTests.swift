#if os(iOS) && !targetEnvironment(macCatalyst)
  import SwiftUI
  import Testing
  import UIKit

  import Textual

  extension StructuredText {
    @MainActor
    struct StreamingParseTests {
      final class CountingParser: MarkupParser {
        private(set) var count = 0
        private let base = AttributedStringMarkdownParser.markdown()

        func attributedString(for input: String) throws -> AttributedString {
          count += 1
          return try base.attributedString(for: input)
        }
      }

      struct Reply: View {
        let text: String
        let parser: CountingParser

        var body: some View {
          StructuredText(text, parser: parser)
        }
      }

      @Test func eachStreamedDeltaParsesOnce() async throws {
        let parser = CountingParser()
        var text = "Starting the reply."
        let host = UIHostingController(rootView: Reply(text: text, parser: parser))
        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 375, height: 2000))
        window.rootViewController = host
        window.isHidden = false
        defer { window.isHidden = true }
        host.view.layoutIfNeeded()

        for delta in 1...60 {
          text += delta % 10 == 0 ? "\n\n- item \(delta)" : " word \(delta)"
          host.rootView = Reply(text: text, parser: parser)
          host.view.setNeedsLayout()
          host.view.layoutIfNeeded()
          try await Task.sleep(for: .milliseconds(5))
        }
        for _ in 0..<5 {
          host.view.layoutIfNeeded()
          try await Task.sleep(for: .milliseconds(20))
        }

        #expect(parser.count == 61)
      }
    }
  }
#endif
