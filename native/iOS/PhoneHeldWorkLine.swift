import SwiftUI

struct PhoneHeldWorkLine: View {
    let line: HeldWorkLine

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(line.id).fontWeight(.semibold).fixedSize()
            Text(line.text).lineLimit(1).truncationMode(.tail)
        }
        .font(.caption)
        .foregroundStyle(PhonePalette.muted)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(line.spoken)
    }
}
