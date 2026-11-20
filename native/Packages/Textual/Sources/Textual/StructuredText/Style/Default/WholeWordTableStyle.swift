import SwiftUI

extension StructuredText {
  /// A table style that wraps cells between words to fit its container, and scrolls horizontally
  /// only when the longest words cannot fit.
  public struct WholeWordTableStyle: TableStyle {
    private static let borderWidth: CGFloat = 1

    /// Creates a whole-word table style.
    public init() {}

    public func makeBody(configuration: Configuration) -> some View {
      Overflow {
        ContainerWidth {
          Color.clear
            .frame(height: 0)
            .containerRelativeFrame(.horizontal)
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
      }
      .textual.tableCellsKeepWords()
      .textual.tableCellSpacing(horizontal: Self.borderWidth, vertical: Self.borderWidth)
      .textual.blockSpacing(.fontScaled(top: 1.6, bottom: 1.6))
    }
  }
}

/// Lays the table out at its container's width, which the first subview reports even inside a
/// horizontal scroll view, or at its narrowest whole-word width when that is wider.
private struct ContainerWidth: Layout {
  func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
    guard subviews.count == 2 else { return .zero }
    return subviews[1].sizeThatFits(table(subviews))
  }

  func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
    guard subviews.count == 2 else { return }
    subviews[0].place(at: bounds.origin, proposal: .zero)
    subviews[1].place(at: bounds.origin, proposal: table(subviews))
  }

  private func table(_ subviews: Subviews) -> ProposedViewSize {
    let container = subviews[0].sizeThatFits(.unspecified).width
    let narrowest = subviews[1].sizeThatFits(ProposedViewSize(width: 0, height: nil)).width
    return ProposedViewSize(width: max(container, narrowest), height: nil)
  }
}

extension StructuredText.TableStyle where Self == StructuredText.WholeWordTableStyle {
  /// A table style that keeps words whole, scrolling horizontally only when they cannot fit.
  public static var wholeWords: Self {
    .init()
  }
}
