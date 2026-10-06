import SwiftUI

struct SharedConversationBanner: View {
    let note: String
    let peers: [SharedProcess]
    var leadingInset: CGFloat = 22
    let onOpen: (String) -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "square.on.square")
                .foregroundStyle(DesktopPalette.accentText)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 5) {
                Text(note)
                if peers.count > 1 {
                    BoundedDock(maxHeight: 120) {
                        VStack(alignment: .leading, spacing: 5) {
                            ForEach(peers) { peer in
                                HStack(spacing: 10) {
                                    Text(SharedConversation.peerLabel(peer))
                                        .foregroundStyle(DesktopPalette.text2)
                                    openButton(peer)
                                }
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if peers.count == 1 {
                openButton(peers[0])
            }
        }
        .font(.system(size: 12.5))
        .padding(.leading, leadingInset)
        .padding(.trailing, 14)
        .padding(.vertical, 9)
        .background(DesktopPalette.dock)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
        .accessibilityElement(children: .contain)
    }

    private func openButton(_ peer: SharedProcess) -> some View {
        Button("Open other") { onOpen(peer.id) }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.accentText)
            .accessibilityLabel(SharedConversation.openLabel(peer))
            .help(SharedConversation.openLabel(peer))
    }
}
