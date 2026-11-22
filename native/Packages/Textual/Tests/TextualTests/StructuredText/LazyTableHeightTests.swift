#if os(macOS)
  import AppKit
  import SwiftUI
  import Testing

  import Textual

  extension StructuredText {
    @MainActor
    struct LazyTableHeightTests {
      static func reply(_ turn: Int) -> String {
        let rows = (0..<12).map { row in
          "| \(row + 1) | Search case \(turn)-\(row) where a matching job title outranks the right city or company | \(row % 3 == 0 ? "Still open, needs a ranking change" : "Fixed") |"
        }.joined(separator: "\n")
        return """
          ## Results for part \(turn)

          The review for part \(turn) is done, and the table lists each case.

          | # | Case | Status |
          |---|---|---|
          \(rows)

          Overall part \(turn) is ready to merge.
          """
      }

      enum Inset {
        case padding, margins
      }

      final class Heights {
        var seen: [Int: [CGFloat]] = [:]
      }

      struct Rows: View {
        let heights: Heights
        let inset: Inset
        let hint: CGFloat?
        static let column: CGFloat = 440
        static let pane: CGFloat = 760
        private var gutter: CGFloat { (Self.pane - Self.column) / 2 }

        var body: some View {
          ScrollView {
            LazyVStack(spacing: 12) {
              ForEach(0..<8, id: \.self) { turn in
                StructuredText(markdown: LazyTableHeightTests.reply(turn))
                  .font(.system(size: 14.5))
                  .textual.tableStyle(.wholeWords)
                  .textual.tableWidthHint(hint)
                  .fixedSize(horizontal: false, vertical: true)
                  .onGeometryChange(for: CGFloat.self) { $0.size.height } action: {
                    heights.seen[turn, default: []].append($0.rounded())
                  }
              }
            }
            .padding(.horizontal, inset == .padding ? gutter : 0)
          }
          .contentMargins(.horizontal, inset == .margins ? gutter : 0, for: .scrollContent)
          .scrollIndicators(.never)
        }
      }

      private func build(_ inset: Inset, hint: CGFloat?) throws -> Heights {
        let heights = Heights()
        let host = NSHostingView(rootView: Rows(heights: heights, inset: inset, hint: hint))
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
        for y in Array(stride(from: 0, through: 6000, by: 500)) + Array(stride(from: 6000, through: 0, by: -500)) {
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

      @Test func aTableInAColumnPaddedInsideItsScrollViewIsBuiltAtOneHeightWithTheColumnAsItsHint() throws {
        let heights = try build(.padding, hint: Rows.column)
        #expect(heights.seen.count >= 4)
        for (turn, seen) in heights.seen {
          #expect(Set(seen).count == 1, "reply \(turn) changed height after it was built: \(seen)")
        }
      }

      @Test func aTableInAColumnSetByContentMarginsIsBuiltAtOneHeightWithoutAHint() throws {
        let heights = try build(.margins, hint: nil)
        #expect(heights.seen.count >= 4)
        for (turn, seen) in heights.seen {
          #expect(Set(seen).count == 1, "reply \(turn) changed height after it was built: \(seen)")
        }
      }

      @Test func withoutAHintATableSettlesWhereTheHintedOneIsBuilt() throws {
        let plain = try build(.padding, hint: nil)
        let hinted = try build(.padding, hint: Rows.column)
        #expect(plain.seen[1]?.last == hinted.seen[1]?.last)
        #expect(Set(plain.seen[1] ?? []).count > 1, "the first pass measures the pane, which is what the hint is for: \(plain.seen[1] ?? [])")
      }
    }
  }
#endif
