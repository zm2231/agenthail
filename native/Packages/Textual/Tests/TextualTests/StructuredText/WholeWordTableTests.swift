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
        var cells: [CellKey: CGFloat]
        var columns: [Int: CGFloat]
      }

      struct CellKey: Hashable {
        let row: Int
        let column: Int
      }

      private final class TableWidth {
        var value: CGFloat = 0
        var cells: [CellKey: CGFloat] = [:]
        var columns: [Int: CGFloat] = [:]
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

      @Test func theGitHubLookKeepsWordsWholeTheSameWay() throws {
        let wide = measure(.gitHubWholeWords, width: 760)
        let plainWide = measure(.gitHub, width: 760)
        #expect(wide.settled.height == plainWide.settled.height)
        #expect(wide.table <= 760)

        let narrow = measure(.gitHubWholeWords, width: 200)
        let plainNarrow = measure(.gitHub, width: 200)
        #expect(narrow.table > 200)
        #expect(narrow.settled.width == 200)
        #expect(narrow.settled.height < plainNarrow.settled.height)
        #expect(narrow.first == narrow.settled)
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

      @Test func wordsStyledLargerThanTheirTextStayWhole() throws {
        let styled = """
          | Code | Strong | Link |
          | --- | --- | --- |
          | `reconfiguration_snapshot` | **Unbreakable** | [documentation](https://example.com) |
          """
        let style = InlineStyle()
          .code(.monospaced, .fontScale(1.25))
          .strong(.fontWeight(.bold), .fontScale(1.3))
          .link(.fontScale(1.3))
        let wide = measure(.wholeWords, width: 900, markdown: styled, inlineStyle: style)
        let narrow = measure(.wholeWords, width: 160, markdown: styled, inlineStyle: style)

        for column in 0..<3 {
          let cell = CellKey(row: 1, column: column)
          #expect(narrow.cells[cell] == wide.cells[cell], "column \(column) wrapped its one word")
        }
        #expect(narrow.table > 160)
      }

      private static let fourColumns = """
        | Session | Surface | Status | Notes on the last turn |
        | --- | --- | --- | --- |
        | core-lead | claude | idle | Finished the cleanup plan and waiting on review of the configuration changes |
        | release-notes | claude | waiting | Needs the person to confirm the changelog wording before publishing |
        """

      @Test(arguments: [260.0, 300.0])
      func aLongTextColumnGetsMoreWidthThanOneWordColumnsWhenWordsCannotFit(width: CGFloat) throws {
        for words in [
          measure(.wholeWords, width: width, fontSize: 15, markdown: Self.fourColumns),
          measure(.gitHubWholeWords, width: width, fontSize: 15, markdown: Self.fourColumns),
        ] {
          let notes = try #require(words.columns[3])
          for column in 0..<3 {
            let short = try #require(words.columns[column])
            #expect(notes > short * 1.25, "notes \(notes) against column \(column) at \(short)")
          }
          #expect(words.table > width)
        }
      }

      @Test func aTableWhoseWordsJustFitKeepsTheColumnWidth() throws {
        let words = measure(.wholeWords, width: 360, fontSize: 15, markdown: Self.fourColumns)
        let plain = measure(.default, width: 360, fontSize: 15, markdown: Self.fourColumns)

        #expect(words.table <= 360)
        #expect(words.columns.count == 4)
        #expect(words.columns == plain.columns)
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
        inlineStyle: InlineStyle = .default,
        then change: ((Column) -> Void)? = nil
      ) -> Measured {
        let tableWidth = TableWidth()
        let column = Column(width: width, fontSize: fontSize, markdown: markdown)
        let host = NSHostingView(rootView: Live(column: column, style: style, inlineStyle: inlineStyle, probe: Probe(width: tableWidth)))
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
        return Measured(
          first: first, settled: host.fittingSize, table: tableWidth.value, cells: tableWidth.cells, columns: tableWidth.columns)
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
        let inlineStyle: InlineStyle
        let probe: Probe

        var body: some View {
          StructuredText(markdown: column.markdown)
            .font(.system(size: column.fontSize))
            .textual.inlineStyle(inlineStyle)
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
            .onGeometryChange(for: CGSize.self) { $0.size } action: {
              width.cells[CellKey(row: configuration.row, column: configuration.column)] = $0.height
              width.columns[configuration.column] = max(width.columns[configuration.column] ?? 0, $0.width)
            }
        }
      }
    }
  }
#endif
