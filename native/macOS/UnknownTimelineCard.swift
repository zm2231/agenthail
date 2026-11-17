import SwiftUI
import Textual

struct UnknownTimelineCard: View {
    let title: String
    let kind: String
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            HStack(spacing: DesktopSpace.s) {
                Text(title)
                    .fontWeight(.medium)
                    .foregroundStyle(DesktopPalette.text)
                    .lineLimit(1)
                Text(kind)
                    .font(.system(size: ReadingType.caption, design: .monospaced))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1)
                    .background(DesktopPalette.fill, in: Capsule())
                Spacer(minLength: 0)
            }
            .font(.system(size: ReadingType.meta))
            if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                Text("No recorded content.")
                    .font(.system(size: ReadingType.meta))
                    .foregroundStyle(DesktopPalette.muted)
            } else {
                StructuredText(markdown: text)
                    .font(.system(size: ReadingType.body))
                    .foregroundStyle(DesktopPalette.text)
                    .desktopInlineStyle()
                    .textual.textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DesktopPalette.dock, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(DesktopPalette.line))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("\(title), \(kind)")
    }
}
