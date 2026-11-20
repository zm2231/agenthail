#if os(macOS)
  import AppKit
  import SwiftUI
  import Testing

  import Textual

  extension StructuredText {
    @MainActor
    struct WholeWordTableTests {
      private static let markdown = """
        Before.

        | Session | Status | Notes on the last turn |
        | --- | --- | --- |
        | release-notes | waiting | Needs the person to confirm the changelog wording before publishing |
        | core-lead | idle | Finished the cleanup plan and waiting on review of the configuration changes |

        After.
        """

      private struct Measured {
        var first: CGSize
        var settled: CGSize
        var table: CGFloat
      }

      private final class TableWidth {
        var value: CGFloat = 0
      }

      @Test func aWideColumnLaysTheTableOutAsTheDefaultStyleDoes() throws {
        let words = measure(.wholeWords, width: 760)
        let plain = measure(.default, width: 760)

        #expect(words.settled.height == plain.settled.height)
        #expect(words.table <= 760)
      }

      @Test func aNarrowColumnKeepsEveryWordWholeAndScrolls() throws {
        let words = measure(.wholeWords, width: 200)
        let plain = measure(.default, width: 200)

        #expect(words.table > 200)
        #expect(words.settled.width == 200)
        #expect(words.settled.height < plain.settled.height)
      }

      @Test(arguments: [200.0, 760.0])
      func theFirstLayoutInAWindowIsTheSettledLayout(width: CGFloat) throws {
        let words = measure(.wholeWords, width: width)

        #expect(words.first == words.settled)
      }

      private func measure(_ style: some TableStyle, width: CGFloat) -> Measured {
        let tableWidth = TableWidth()
        let view = StructuredText(markdown: Self.markdown)
          .textual.tableStyle(style)
          .textual.tableCellStyle(Probe(width: tableWidth))
          .fixedSize(horizontal: false, vertical: true)
          .frame(width: width)
        let host = NSHostingView(rootView: view)
        let window = NSWindow(
          contentRect: NSRect(x: 0, y: 0, width: width, height: 400),
          styleMask: [.borderless],
          backing: .buffered,
          defer: false
        )
        window.contentView = host
        let first = host.fittingSize
        for _ in 0..<5 {
          host.layoutSubtreeIfNeeded()
          RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        }
        return Measured(first: first, settled: host.fittingSize, table: tableWidth.value)
      }

      private struct Probe: TableCellStyle {
        let width: TableWidth

        func makeBody(configuration: Configuration) -> some View {
          configuration.label
            .padding(6)
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .global).maxX } action: {
              width.value = max(width.value, $0)
            }
        }
      }
    }
  }
#endif
