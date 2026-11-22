import SwiftUI

struct HeldWorkRowLine: View {
    let line: HeldWorkLine

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(line.id)
                .fontWeight(.semibold)
                .fixedSize()
            Text(line.text)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .font(.system(size: DesktopType.meta))
        .foregroundStyle(DesktopPalette.text2)
        .help(line.help)
    }
}
