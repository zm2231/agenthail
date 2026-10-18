import SwiftUI

struct SlashCommandMenu: View {
    struct Suggestion: Identifiable, Equatable {
        let id: String
        let title: String
        let detail: String
        let insertion: String
    }

    let suggestions: [Suggestion]
    var roundedTop = true
    let choose: (String) -> Void

    var body: some View {
        let shape = UnevenRoundedRectangle(topLeadingRadius: roundedTop ? 14 : 0, topTrailingRadius: roundedTop ? 14 : 0)
        VStack(spacing: 0) {
            ForEach(suggestions) { suggestion in
                Button {
                    choose(suggestion.insertion)
                } label: {
                    HStack(spacing: 10) {
                        Text(suggestion.title)
                            .foregroundStyle(DesktopPalette.text)
                        Text(suggestion.detail)
                            .foregroundStyle(DesktopPalette.text2)
                            .lineLimit(1)
                            .truncationMode(.tail)
                        Spacer(minLength: 0)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .font(.system(size: 13))
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
            }
        }
        .padding(.vertical, 4)
        .background(DesktopPalette.dock, in: shape)
        .overlay(shape.strokeBorder(DesktopPalette.line))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Slash commands")
    }
}
