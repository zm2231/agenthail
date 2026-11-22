import SwiftUI

extension StructuredText {
  /// A table style that wraps cells between words to fit its container, and scrolls horizontally
  /// only when the longest words cannot fit.
  public struct WholeWordTableStyle: TableStyle {
    private static let borderWidth: CGFloat = 1

    /// Creates a whole-word table style.
    public init() {}

    public func makeBody(configuration: Configuration) -> some View {
      WholeWordTable {
        configuration.label
          .fixedSize(horizontal: false, vertical: true)
          .textual.tableOverlay { layout in
            Canvas { context, _ in
              for divider in layout.dividers() {
                context.fill(
                  Path(divider),
                  with: .style(DynamicColor.grayTertiary)
                )
              }
            }
          }
          .padding(Self.borderWidth)
      }
      .textual.tableCellSpacing(horizontal: Self.borderWidth, vertical: Self.borderWidth)
      .textual.blockSpacing(.fontScaled(top: 1.6, bottom: 1.6))
    }
  }
}

/// Scrolls a table horizontally only when its cells, each kept as wide as its longest word, cannot
/// fit the container.
struct WholeWordTable<Content: View>: View {
  @State private var offered = OfferedWidth()
  @ViewBuilder let content: Content

  var body: some View {
    OfferedWidthReader(offered: offered) {
      Overflow {
        ContainerWidth(offered: offered) {
          Color.clear
            .frame(height: 0)
            .containerRelativeFrame(.horizontal)
          content
        }
      }
    }
    .textual.tableCellsKeepWords()
  }
}

/// The width offered to the table from outside its horizontal scroll view, which lays the table
/// out inside the same layout call, so the table reads it before its scroll view has a size.
final class OfferedWidth {
  var value: CGFloat?
}

private struct OfferedWidthReader: Layout {
  let offered: OfferedWidth

  func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
    guard let subview = subviews.first else { return .zero }
    offered.value = proposal.width
    return subview.sizeThatFits(proposal)
  }

  func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
    guard let subview = subviews.first else { return }
    offered.value = bounds.width
    subview.place(at: bounds.origin, proposal: ProposedViewSize(bounds.size))
  }
}

/// Lays the table out at its container's width, which the first subview reports even inside a
/// horizontal scroll view, or at its narrowest whole-word width when that is wider.
private struct ContainerWidth: Layout {
  let offered: OfferedWidth

  struct Extent {
    var narrowest: CGFloat
    var widest: CGFloat
  }

  static let roomForLongText: CGFloat = 1.3

  func makeCache(subviews: Subviews) -> Extent? { nil }

  func updateCache(_ cache: inout Extent?, subviews: Subviews) {
    cache = nil
  }

  func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout Extent?) -> CGSize {
    guard subviews.count == 2 else { return .zero }
    return subviews[1].sizeThatFits(table(subviews, cache: &cache))
  }

  func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout Extent?) {
    guard subviews.count == 2 else { return }
    subviews[0].place(at: bounds.origin, proposal: .zero)
    subviews[1].place(at: bounds.origin, proposal: table(subviews, cache: &cache))
  }

  private func table(_ subviews: Subviews, cache: inout Extent?) -> ProposedViewSize {
    let container = offered.value.flatMap { $0 > 0 && $0.isFinite ? $0 : nil } ?? subviews[0].sizeThatFits(.unspecified).width
    let extent =
      cache
      ?? Extent(
        narrowest: subviews[1].sizeThatFits(ProposedViewSize(width: 0, height: nil)).width,
        widest: subviews[1].sizeThatFits(.unspecified).width
      )
    cache = extent
    guard extent.narrowest > container else { return ProposedViewSize(width: container, height: nil) }
    let comfortable = min(extent.widest, extent.narrowest * Self.roomForLongText)
    return ProposedViewSize(width: max(container, comfortable), height: nil)
  }
}

extension StructuredText.TableStyle where Self == StructuredText.WholeWordTableStyle {
  /// A table style that keeps words whole, scrolling horizontally only when they cannot fit.
  public static var wholeWords: Self {
    .init()
  }
}
