import AppKit
import SwiftUI

struct AttachmentChips: View {
    @Binding var attachments: [ComposerAttachment]

    var body: some View {
        ChipFlow(spacing: DesktopSpace.xs) {
            ForEach(attachments) { attachment in
                HStack(spacing: DesktopSpace.xs) {
                    AttachmentThumbnail(attachment: attachment, side: 18)
                    Text(attachment.name)
                        .foregroundStyle(DesktopPalette.text)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .frame(maxWidth: 220, alignment: .leading)
                        .fixedSize(horizontal: true, vertical: false)
                    Button {
                        attachments.removeAll { $0.id == attachment.id }
                    } label: {
                        Image(systemName: "xmark")
                            .font(.system(size: DesktopType.caption, weight: .semibold))
                            .frame(width: 14, height: 14)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityLabel("Remove \(attachment.name)")
                }
                .font(.system(size: DesktopType.meta))
                .padding(.leading, 3)
                .padding(.trailing, 6)
                .frame(height: 24)
                .background(DesktopPalette.dock, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control).strokeBorder(DesktopPalette.line))
                .help("\(attachment.name), \(ByteCountFormatter.string(fromByteCount: Int64(attachment.data.count), countStyle: .file))")
            }
        }
    }
}

struct AttachmentThumbnail: View {
    let attachment: ComposerAttachment
    let side: CGFloat

    var body: some View {
        if attachment.isImage, let image = NSImage(data: attachment.data) {
            Image(nsImage: image)
                .resizable()
                .scaledToFill()
                .frame(width: side, height: side)
                .clipShape(RoundedRectangle(cornerRadius: DesktopRadius.control))
                .accessibilityHidden(true)
        } else {
            Image(systemName: "doc")
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.text2)
                .frame(width: side, height: side)
        }
    }
}
