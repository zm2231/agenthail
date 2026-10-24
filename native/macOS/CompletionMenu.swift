import SwiftUI

struct CompletionMenu: View {
    let suggestions: [CompletionItem]
    var notice: String? = nil
    var highlighted = 0
    var roundedTop = true
    let choose: (CompletionItem) -> Void

    private static let visibleRows = 8

    var body: some View {
        let shape = UnevenRoundedRectangle(topLeadingRadius: roundedTop ? 14 : 0, topTrailingRadius: roundedTop ? 14 : 0)
        ScrollViewReader { proxy in
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(Array(suggestions.enumerated()), id: \.element.id) { index, suggestion in
                        row(suggestion, highlighted: index == highlighted)
                            .id(suggestion.id)
                    }
                    if let notice {
                        Text(notice)
                            .font(.system(size: DesktopType.body))
                            .foregroundStyle(DesktopPalette.text2)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.horizontal, 14)
                            .padding(.vertical, 6)
                    }
                }
                .padding(.vertical, DesktopSpace.xs)
            }
            .scrollBounceBehavior(.basedOnSize)
            .frame(maxHeight: CGFloat(Self.visibleRows) * 30 + DesktopSpace.xs * 2)
            .fixedSize(horizontal: false, vertical: suggestions.count + (notice == nil ? 0 : 1) <= Self.visibleRows)
            .onChange(of: highlighted) { _, index in
                guard suggestions.indices.contains(index) else { return }
                proxy.scrollTo(suggestions[index].id)
            }
        }
        .background(DesktopPalette.dock, in: shape)
        .overlay(shape.strokeBorder(DesktopPalette.line))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Suggestions")
    }

    private func row(_ suggestion: CompletionItem, highlighted: Bool) -> some View {
        Button {
            choose(suggestion)
        } label: {
            HStack(spacing: 10) {
                Text(suggestion.title)
                    .foregroundStyle(DesktopPalette.text)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(suggestion.detail)
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(1)
                    .truncationMode(.tail)
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 6)
            .background(DesktopPalette.selection.when(highlighted))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .font(.system(size: DesktopType.body))
        .accessibilityAddTraits(highlighted ? .isSelected : [])
    }
}
