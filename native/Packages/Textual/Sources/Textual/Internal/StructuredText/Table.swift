import SwiftUI

// MARK: - Overview
//
// Table uses a two-pass layout system. The first pass renders cells which emit their bounds via
// preferences. The second pass collects all cell bounds, transforms them from anchor coordinates
// to geometry coordinates, and builds a `TableLayout` that the style uses to render overlays
// (such as grid lines) and backgrounds with precise cell positions.

extension StructuredText {
  struct Table: View {
    @Environment(\.tableStyle) private var tableStyle

    @State private var reported: TableCell.Spacing?

    private let intent: PresentationIntent.IntentType?
    private let content: AttributedSubstring
    private let columns: [PresentationIntent.TableColumn]

    init(
      intent: PresentationIntent.IntentType?,
      content: AttributedSubstring,
      columns: [PresentationIntent.TableColumn]
    ) {
      self.intent = intent
      self.content = content
      self.columns = columns
    }

    var body: some View {
      // A stand-in resolution exposes the style's cell spacing before the real grid is built.
      Group(subviews: styled(label: Color.clear)) { declaration in
        let declared = declaration.lazy.compactMap(\.containerValues.tableCellSpacing).last
        styled(label: label(spacing: reported ?? declared ?? TableCell.Spacing()))
          .onPreferenceChange(TableCell.SpacingKey.self) { @MainActor in
            reported = $0
          }
      }
    }

    private func styled(label: some View) -> AnyView {
      let configuration = TableStyleConfiguration(label: .init(label), indentationLevel: indentationLevel)
      let resolvedStyle = tableStyle.resolve(configuration: configuration)
      return AnyView(resolvedStyle)
    }

    @ViewBuilder
    private func label(spacing: TableCell.Spacing) -> some View {
      let rowRuns = content.blockRuns(parent: intent)

      Grid(horizontalSpacing: spacing.horizontal, verticalSpacing: spacing.vertical) {
        ForEach(rowRuns.indices, id: \.self) { rowIndex in
          let rowRun = rowRuns[rowIndex]
          let rowContent = content[rowRun.range]
          let columnRuns = rowContent.blockRuns(parent: rowRun.intent)

          GridRow {
            ForEach(columnRuns.indices, id: \.self) { columnIndex in
              let cellRun = columnRuns[columnIndex]
              let cellContent = rowContent[cellRun.range]

              TableCell(cellContent, row: rowIndex, column: columnIndex)
                .gridColumnAlignment(alignment(for: columnIndex))
            }
          }
        }
      }
    }

    private var indentationLevel: Int {
      content.runs.first?.presentationIntent?.indentationLevel ?? 0
    }

    private func alignment(for columnIndex: Int) -> HorizontalAlignment {
      guard columnIndex < columns.count else {
        return .leading
      }

      switch columns[columnIndex].alignment {
      case .left:
        return .leading
      case .center:
        return .center
      case .right:
        return .trailing
      @unknown default:
        return .leading
      }
    }
  }
}

extension ContainerValues {
  @Entry var tableCellSpacing: StructuredText.TableCell.Spacing? = nil
}
