import SwiftUI

struct LeadReplacementBanner: View {
    let replacement: LeadReplacement
    let acknowledge: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "arrow.triangle.2.circlepath")
                .foregroundStyle(DesktopPalette.attention)
            VStack(alignment: .leading, spacing: 3) {
                Text("@\(replacement.name) runs in this new conversation")
                    .font(.system(size: DesktopType.body, weight: .medium))
                Text(replacement.reason)
                    .lineLimit(2)
                    .foregroundStyle(DesktopPalette.text2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Button("Acknowledge", action: acknowledge)
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.accentText)
        }
        .font(.system(size: DesktopType.body))
        .padding(.horizontal, DesktopSpace.m)
        .padding(.vertical, 9)
        .background(DesktopPalette.attentionBackground, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(DesktopPalette.attentionLine))
    }
}
