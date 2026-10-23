import SwiftUI

struct CompletionRow: View {
    let suggestions: [CompletionItem]
    var notice: String? = nil
    let choose: (CompletionItem) -> Void
    let dismiss: () -> Void

    var body: some View {
        HStack(spacing: 4) {
            ScrollView(.horizontal) {
                HStack(spacing: 8) {
                    ForEach(suggestions) { suggestion in
                        Button { choose(suggestion) } label: { chip(suggestion) }
                            .buttonStyle(.plain)
                            .accessibilityLabel(suggestion.title)
                            .accessibilityHint(suggestion.detail)
                    }
                    if let notice {
                        Text(notice).font(.footnote).foregroundStyle(.secondary)
                            .lineLimit(2)
                            .frame(maxWidth: 260, minHeight: 44, alignment: .leading)
                    }
                }
                .padding(.horizontal, 8)
            }
            .scrollIndicators(.hidden)
            .mask {
                HStack(spacing: 0) {
                    Rectangle()
                    LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing).frame(width: 16)
                }
            }
            Button(action: dismiss) {
                Image(systemName: "xmark").font(.footnote.weight(.semibold)).foregroundStyle(.secondary)
                    .frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Hide suggestions")
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Suggestions")
        .accessibilityIdentifier("composer-suggestions")
    }

    private func chip(_ suggestion: CompletionItem) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(suggestion.title).font(.subheadline.weight(.medium)).foregroundStyle(.primary)
                .lineLimit(1).truncationMode(.middle)
            if !suggestion.detail.isEmpty {
                Text(suggestion.detail).font(.caption).foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.tail)
            }
        }
        .frame(maxWidth: 220, alignment: .leading)
        .padding(.horizontal, 12).padding(.vertical, 5)
        .frame(minHeight: 44)
        .background(Color.primary.opacity(0.07), in: RoundedRectangle(cornerRadius: 12))
        .contentShape(RoundedRectangle(cornerRadius: 12))
    }
}
