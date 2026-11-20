import SwiftUI

extension StructuredText {
  struct TableCell: View {
    @Environment(\.tableCellStyle) private var tableCellStyle
    @Environment(\.tableCellsKeepWords) private var keepsWords

    private let content: AttributedSubstring
    private let identifier: TableCell.Identifier

    init(_ content: AttributedSubstring, row: Int, column: Int) {
      self.content = content
      self.identifier = .init(row: row, column: column)
    }

    var body: some View {
      let configuration = TableCellStyleConfiguration(
        label: .init(label),
        indentationLevel: indentationLevel,
        row: identifier.row,
        column: identifier.column
      )
      let resolvedStyle =
        tableCellStyle
        .resolve(configuration: configuration)
        .anchorPreference(key: BoundsKey.self, value: .bounds) { anchor in
          [identifier: anchor]
        }

      AnyView(resolvedStyle)
    }

    @ViewBuilder
    private var label: some View {
      let text = WithInlineStyle(AttributedString(content)) {
        TextFragment($0)
      }
      if keepsWords {
        WholeWords {
          Text(Self.wordPerLine(AttributedString(content)))
            .hidden()
            .accessibilityHidden(true)
          text
        }
      } else {
        text
      }
    }

    static func wordPerLine(_ text: AttributedString) -> AttributedString {
      var text = text
      let spaces = text.characters.indices.filter { text.characters[$0] == " " || text.characters[$0] == "\t" }
      for index in spaces.reversed() {
        text.characters.replaceSubrange(index...index, with: "\n")
      }
      return text
    }

    private var indentationLevel: Int {
      content.presentationIntent?.indentationLevel ?? 0
    }
  }
}

extension EnvironmentValues {
  @Entry var tableCellsKeepWords = false
}

/// Lays out its second subview no narrower than the first, a copy of the text with one word per
/// line, so the text wraps between words and its height matches the width it is placed at.
private struct WholeWords: Layout {
  func makeCache(subviews: Subviews) -> CGFloat? { nil }

  func updateCache(_ cache: inout CGFloat?, subviews: Subviews) {
    cache = nil
  }

  func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout CGFloat?) -> CGSize {
    guard subviews.count == 2 else { return .zero }
    let words = cache ?? subviews[0].sizeThatFits(.unspecified).width
    cache = words
    let width = proposal.width.map { max($0, words) }
    let text = subviews[1].sizeThatFits(ProposedViewSize(width: width, height: nil))
    return CGSize(width: max(text.width, words), height: text.height)
  }

  func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout CGFloat?) {
    guard subviews.count == 2 else { return }
    subviews[0].place(at: bounds.origin, proposal: .zero)
    subviews[1].place(at: bounds.origin, proposal: ProposedViewSize(width: bounds.width, height: bounds.height))
  }
}

extension StructuredText.TableCell {
  struct Identifier: Hashable {
    let row: Int
    let column: Int
  }

  struct BoundsKey: PreferenceKey {
    static let defaultValue: [Identifier: Anchor<CGRect>] = [:]

    static func reduce(
      value: inout [Identifier: Anchor<CGRect>],
      nextValue: () -> [Identifier: Anchor<CGRect>]
    ) {
      value.merge(nextValue(), uniquingKeysWith: { $1 })
    }
  }
}

extension StructuredText.TableCell {
  struct Spacing: Sendable, Hashable {
    let horizontal: CGFloat?
    let vertical: CGFloat?

    init(horizontal: CGFloat? = nil, vertical: CGFloat? = nil) {
      self.horizontal = horizontal
      self.vertical = vertical
    }
  }

  struct SpacingKey: PreferenceKey {
    static let defaultValue = Spacing()

    static func reduce(value: inout Spacing, nextValue: () -> Spacing) {
      value = nextValue()
    }
  }
}
