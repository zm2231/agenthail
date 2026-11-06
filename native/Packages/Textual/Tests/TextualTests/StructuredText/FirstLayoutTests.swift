#if os(iOS) && !targetEnvironment(macCatalyst)
  import SwiftUI
  import Testing
  import UIKit

  import Textual

  extension StructuredText {
    @MainActor
    struct FirstLayoutTests {
      enum Sample: String, CaseIterable {
        case headings, nestedList, orderedList, quote, code, table, document

        var markdown: String {
          switch self {
          case .headings: "# Title\n\nA paragraph.\n\n## Section\n\nAnother paragraph."
          case .nestedList: "Before.\n\n- One\n  - Two\n    - Three\n- Four\n\nAfter."
          case .orderedList: "Before.\n\n1. One\n2. Two\n3. Three\n\nAfter."
          case .quote: "Before.\n\n> Quoted text.\n>\n> - Item\n\nAfter."
          case .code: "Before.\n\n```swift\nlet x = 1\n```\n\nAfter."
          case .table: "Before.\n\n| A | B |\n| --- | --- |\n| 1 | 2 |\n\nAfter."
          case .document: MixedDocumentTests.markdown
          }
        }
      }

      private let fitting = CGSize(width: 375, height: CGFloat.greatestFiniteMagnitude)

      struct WideTableStyle: TableStyle {
        func makeBody(configuration: Configuration) -> some View {
          configuration.label.textual.tableCellSpacing(horizontal: 10, vertical: 30)
        }
      }

      @Test(arguments: Sample.allCases, [false, true])
      func firstLayoutIsTheSettledLayout(sample: Sample, gitHub: Bool) async throws {
        let (first, settled) = try await layouts(of: document(sample.markdown, gitHub: gitHub))

        #expect(settled.height > 40)
        #expect(first == settled)
      }

      @Test func aStyleOwnCellSpacingIsInTheFirstLayout() async throws {
        let (first, settled) = try await layouts(
          of: StructuredText(markdown: Sample.table.markdown).textual.tableStyle(WideTableStyle())
        )
        let (_, plain) = try await layouts(of: StructuredText(markdown: Sample.table.markdown))

        #expect(settled.height != plain.height)
        #expect(first == settled)
      }

      private func layouts(of view: some View) async throws -> (first: CGSize, settled: CGSize) {
        let host = UIHostingController(rootView: view)
        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 375, height: 2000))
        window.rootViewController = host
        window.isHidden = false
        defer { window.isHidden = true }

        let first = host.sizeThatFits(in: fitting)
        for _ in 0..<10 {
          host.view.setNeedsLayout()
          host.view.layoutIfNeeded()
          try await Task.sleep(for: .milliseconds(30))
        }
        return (first, host.sizeThatFits(in: fitting))
      }

      @ViewBuilder private func document(_ markdown: String, gitHub: Bool) -> some View {
        if gitHub {
          StructuredText(markdown: markdown).textual.structuredTextStyle(.gitHub)
        } else {
          StructuredText(markdown: markdown)
        }
      }
    }
  }
#endif
