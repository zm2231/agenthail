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

      @Test func resizingTheSameTableFollowsTheNewColumnWidth() throws {
        let wide = measure(.wholeWords, width: 760) { $0.width = 200 }
        let narrow = measure(.wholeWords, width: 200)

        #expect(wide.settled == narrow.settled)
        #expect(wide.table > 200)
      }

      @Test func aLargerFontOrALongerWordWidensTheSameTable() throws {
        let larger = measure(.wholeWords, width: 200) { $0.fontSize = 26 }
        let freshLarger = measure(.wholeWords, width: 200, fontSize: 26)
        #expect(larger.settled == freshLarger.settled)
        #expect(larger.table == freshLarger.table)

        let longer = Self.markdown.replacingOccurrences(of: "configuration", with: "reconfigurationsnapshotting")
        let streamed = measure(.wholeWords, width: 200) { $0.markdown = longer }
        let freshStreamed = measure(.wholeWords, width: 200, markdown: longer)
        #expect(streamed.settled == freshStreamed.settled)
        #expect(streamed.table == freshStreamed.table)
        #expect(streamed.table > measure(.wholeWords, width: 200).table)
      }

      @Test(arguments: [200.0, 760.0])
      func theFirstLayoutInAWindowIsTheSettledLayout(width: CGFloat) throws {
        let words = measure(.wholeWords, width: width)

        #expect(words.first == words.settled)
      }

      private func measure(
        _ style: some TableStyle,
        width: CGFloat,
        fontSize: CGFloat = 13,
        markdown: String = Self.markdown,
        then change: ((Column) -> Void)? = nil
      ) -> Measured {
        let tableWidth = TableWidth()
        let column = Column(width: width, fontSize: fontSize, markdown: markdown)
        let host = NSHostingView(rootView: Live(column: column, style: style, probe: Probe(width: tableWidth)))
        let window = NSWindow(
          contentRect: NSRect(x: 0, y: 0, width: width, height: 400),
          styleMask: [.borderless],
          backing: .buffered,
          defer: false
        )
        window.contentView = host
        let first = host.fittingSize
        if let change {
          for _ in 0..<5 {
            host.layoutSubtreeIfNeeded()
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
          }
          tableWidth.value = 0
          change(column)
        }
        for _ in 0..<5 {
          host.layoutSubtreeIfNeeded()
          RunLoop.main.run(until: Date().addingTimeInterval(0.05))
        }
        return Measured(first: first, settled: host.fittingSize, table: tableWidth.value)
      }

      @Observable
      final class Column {
        var width: CGFloat
        var fontSize: CGFloat
        var markdown: String

        init(width: CGFloat, fontSize: CGFloat, markdown: String) {
          self.width = width
          self.fontSize = fontSize
          self.markdown = markdown
        }
      }

      private struct Live<Style: TableStyle>: View {
        let column: Column
        let style: Style
        let probe: Probe

        var body: some View {
          StructuredText(markdown: column.markdown)
            .font(.system(size: column.fontSize))
            .textual.tableStyle(style)
            .textual.tableCellStyle(probe)
            .fixedSize(horizontal: false, vertical: true)
            .frame(width: column.width)
        }
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
