import SwiftUI

extension StructuredText {
  struct BlockSpacingKey: PreferenceKey, LayoutValueKey {
    static let defaultValue = BlockSpacing()

    static func reduce(value: inout BlockSpacing, nextValue: () -> BlockSpacing) {
      value = value.union(nextValue())
    }
  }
}

// Readable in the first layout pass; the preference still wins where this cannot reach.
extension ContainerValues {
  @Entry var blockSpacing: StructuredText.BlockSpacing? = nil
}

extension StructuredText.BlockSpacing {
  static func merged(_ values: [StructuredText.BlockSpacing?]) -> StructuredText.BlockSpacing? {
    let present = values.compactMap(\.self)
    guard let first = present.first else { return nil }
    return present.dropFirst().reduce(first) { $0.union($1) }
  }
}

extension StructuredText {
  struct DeclaredBlockSpacing: ViewModifier {
    let blockSpacing: BlockSpacing?

    func body(content: Content) -> some View {
      Group(subviews: content) { subviews in
        ForEach(subviews) { subview in
          subview.containerValue(
            \.blockSpacing,
            BlockSpacing.merged([blockSpacing, subview.containerValues.blockSpacing])
          )
        }
      }
    }
  }

  // A style's stack stops container values, so the nested block's spacing is carried past it.
  struct ForwardsBlockSpacing<Inner: View, Styled: View>: View {
    let inner: Inner
    let styled: (SubviewsCollection) -> Styled

    init(inner: Inner, @ViewBuilder styled: @escaping (SubviewsCollection) -> Styled) {
      self.inner = inner
      self.styled = styled
    }

    var body: some View {
      Group(subviews: inner) { block in
        styled(block)
          .modifier(
            DeclaredBlockSpacing(
              blockSpacing: BlockSpacing.merged(block.map(\.containerValues.blockSpacing))
            )
          )
      }
    }
  }
}
