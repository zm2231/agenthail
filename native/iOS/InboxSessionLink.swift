import SwiftUI

struct InboxSessionLink: View {
    let title: String
    let identifier: String
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack {
                Text(title).font(.headline)
                Spacer(minLength: 8)
                Image(systemName: "chevron.right").font(.caption)
            }
            .frame(minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(identifier)
    }
}
