#if os(iOS) && !targetEnvironment(macCatalyst)
  import SwiftUI
  import Testing
  import UIKit

  import Textual

  extension StructuredText {
    @MainActor
    struct LazyTableHeightPhoneTests {
      enum Placement: String, CaseIterable {
        case reply, listItem, quote, card

        func markdown(_ turn: Int) -> String {
          let table = ["| # | Case | Status |", "|---|---|---|"]
            + (0..<12).map { row in
              "| \(row + 1) | Search case \(turn)-\(row) where a matching job title outranks the right city or company | \(row % 3 == 0 ? "Still open, needs a ranking change" : "Fixed") |"
            }
          switch self {
          case .reply, .card:
            return "## Results for part \(turn)\n\nThe table lists each case.\n\n" + table.joined(separator: "\n") + "\n\nReady to merge."
          case .listItem: return "- Results for part \(turn):\n\n" + table.map { "  " + $0 }.joined(separator: "\n")
          case .quote: return "> Results for part \(turn):\n>\n" + table.map { "> " + $0 }.joined(separator: "\n")
          }
        }
      }

      final class Heights {
        var seen: [Int: [CGFloat]] = [:]
      }

      struct Rows: View {
        let heights: Heights
        let placement: Placement
        let gutter: CGFloat

        var body: some View {
          ScrollView {
            LazyVStack(spacing: 12) {
              ForEach(0..<8, id: \.self) { turn in
                StructuredText(markdown: placement.markdown(turn))
                  .font(.system(size: 16))
                  .textual.tableStyle(.gitHubWholeWords)
                  .fixedSize(horizontal: false, vertical: true)
                  .padding(placement == .card ? 12 : 0)
                  .onGeometryChange(for: CGFloat.self) { $0.size.height } action: {
                    heights.seen[turn, default: []].append($0.rounded())
                  }
              }
            }
            .padding(.horizontal, gutter)
          }
        }
      }

      private func build(_ placement: Placement, gutter: CGFloat = 16) throws -> Heights {
        let heights = Heights()
        let host = UIHostingController(rootView: Rows(heights: heights, placement: placement, gutter: gutter))
        let window = UIWindow(frame: CGRect(x: 0, y: 0, width: 393, height: 852))
        window.rootViewController = host
        window.isHidden = false
        defer { window.isHidden = true }
        func settle() {
          for _ in 0..<6 {
            host.view.layoutIfNeeded()
            RunLoop.main.run(until: Date().addingTimeInterval(0.05))
          }
        }
        settle()
        let scroll = try #require(Self.scrollView(in: host.view))
        for y in Array(stride(from: 0, through: 6000, by: 500)) + Array(stride(from: 6000, through: 0, by: -500)) {
          scroll.setContentOffset(CGPoint(x: 0, y: CGFloat(y)), animated: false)
          settle()
        }
        return heights
      }

      private static func scrollView(in view: UIView) -> UIScrollView? {
        if let scroll = view as? UIScrollView, scroll.contentSize.height > 2000 { return scroll }
        for child in view.subviews {
          if let found = scrollView(in: child) { return found }
        }
        return nil
      }

      @Test(arguments: Placement.allCases)
      func aTableOnAPhoneIsBuiltAtTheHeightItKeeps(placement: Placement) throws {
        let heights = try build(placement)
        print("phone", placement, heights.seen.sorted { $0.key < $1.key }.map { "\($0.key): \($0.value)" })
        #expect(heights.seen.count >= 3)
        for (turn, seen) in heights.seen {
          #expect(Set(seen).count == 1, "\(placement) \(turn) changed height after it was built: \(seen)")
        }
      }
    }
  }
#endif
