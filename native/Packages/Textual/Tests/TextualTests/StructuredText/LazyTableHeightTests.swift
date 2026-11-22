#if os(macOS)
  import AppKit
  import SwiftUI
  import Testing

  import Textual

  extension StructuredText {
    @MainActor
    struct LazyTableHeightTests {
      nonisolated static func table(_ turn: Int) -> [String] {
        ["| # | Case | Status |", "|---|---|---|"]
          + (0..<12).map { row in
            "| \(row + 1) | Search case \(turn)-\(row) where a matching job title outranks the right city or company | \(row % 3 == 0 ? "Still open, needs a ranking change" : "Fixed") |"
          }
      }

      enum Placement: String, CaseIterable {
        case reply, listItem, quote, card, wideCard

        func markdown(_ turn: Int) -> String {
          let table = LazyTableHeightTests.table(turn)
          switch self {
          case .reply, .card, .wideCard:
            return "## Results for part \(turn)\n\nThe table lists each case.\n\n" + table.joined(separator: "\n") + "\n\nReady to merge."
          case .listItem: return "- Results for part \(turn):\n\n" + table.map { "  " + $0 }.joined(separator: "\n")
          case .quote: return "> Results for part \(turn):\n>\n" + table.map { "> " + $0 }.joined(separator: "\n")
          }
        }

        var inset: CGFloat {
          switch self {
          case .card: 14
          case .wideCard: 70
          default: 0
          }
        }
      }

      final class Heights {
        var seen: [Int: [CGFloat]] = [:]
      }

      struct Rows: View {
        let heights: Heights
        let placement: Placement
        let column: CGFloat
        static let pane: CGFloat = 760

        var body: some View {
          ScrollView {
            LazyVStack(spacing: 12) {
              ForEach(0..<8, id: \.self) { turn in
                StructuredText(markdown: placement.markdown(turn))
                  .font(.system(size: 14.5))
                  .textual.tableStyle(.wholeWords)
                  .fixedSize(horizontal: false, vertical: true)
                  .padding(placement.inset)
                  .onGeometryChange(for: CGFloat.self) { $0.size.height } action: {
                    heights.seen[turn, default: []].append($0.rounded())
                  }
              }
            }
            .padding(.horizontal, (Self.pane - column) / 2)
          }
          .scrollIndicators(.never)
        }
      }

      private func build(_ placement: Placement, column: CGFloat = 440) throws -> Heights {
        let heights = Heights()
        let host = NSHostingView(rootView: Rows(heights: heights, placement: placement, column: column))
        let window = NSWindow(
          contentRect: NSRect(x: 0, y: 0, width: Rows.pane, height: 834), styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = host
        func settle() {
          for _ in 0..<6 {
            host.layoutSubtreeIfNeeded()
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
          }
        }
        settle()
        let scroll = try #require(Self.scrollView(in: host))
        for y in Array(stride(from: 0, through: 5000, by: 500)) + Array(stride(from: 5000, through: 0, by: -500)) {
          scroll.contentView.scroll(to: NSPoint(x: 0, y: CGFloat(y)))
          scroll.reflectScrolledClipView(scroll.contentView)
          settle()
        }
        return heights
      }

      private static func scrollView(in view: NSView) -> NSScrollView? {
        if let scroll = view as? NSScrollView, (scroll.documentView?.frame.height ?? 0) > 2000 { return scroll }
        for child in view.subviews {
          if let found = scrollView(in: child) { return found }
        }
        return nil
      }

      @Test(arguments: Placement.allCases)
      func aTableInAPaddedColumnIsBuiltAtTheHeightItKeeps(placement: Placement) throws {
        let heights = try build(placement)
        #expect(heights.seen.count >= 3)
        for (turn, seen) in heights.seen {
          #expect(Set(seen).count == 1, "\(placement) \(turn) changed height after it was built: \(seen)")
        }
      }

      @Test(arguments: Placement.allCases)
      func theSameTableIsADifferentHeightAtTheFullWidthSoAFirstPassThereWouldShow(placement: Placement) throws {
        let column = try #require(build(placement).seen[1]?.first)
        let pane = try #require(build(placement, column: Rows.pane).seen[1]?.first)
        #expect(column != pane, "\(placement) is \(column) in the column and \(pane) across the pane")
      }
    }
  }
#endif
