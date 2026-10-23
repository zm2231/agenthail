import SwiftUI

struct InspectorAgentName: View {
    let name: String
    let rename: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text("Agent").foregroundStyle(DesktopPalette.text2)
                Spacer(minLength: DesktopSpace.s)
                Button("Rename", action: rename)
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accentText)
            }
            Text(name)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

struct InspectorPathRow: View {
    let label: String
    let path: String

    var body: some View {
        GridRow {
            Text(label).foregroundStyle(DesktopPalette.text2)
            Text((path as NSString).abbreviatingWithTildeInPath)
                .font(.system(size: DesktopType.meta, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
                .textSelection(.enabled)
                .help(path)
        }
    }
}
