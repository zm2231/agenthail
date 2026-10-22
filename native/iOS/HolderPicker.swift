import SwiftUI

/// Shown only for a Claude conversation open in several terminals: the daemon
/// refuses any write to it that does not name one.
struct IOSHolderPicker: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState

    private var holders: [SessionHolder] { session.sharedHolders }
    private var picked: SessionHolder? {
        guard let pin = model.holderPicks[session.id] else { return nil }
        return holders.first { HolderPin($0) == pin }
    }

    var body: some View {
        Menu {
            Picker("Terminal", selection: Binding(
                get: { picked?.pid },
                set: { pid in model.pickHolder(holders.first { $0.pid == pid }, for: session.id) }
            )) {
                Text("Choose a terminal").tag(nil as Int?)
                ForEach(holders) { holder in
                    Text(holder.label).tag(holder.pid as Int?)
                }
            }
        } label: {
            Label {
                Text(picked.map { "Sending to \($0.label)" } ?? "Open in \(holders.count) terminals. Choose one")
                    .lineLimit(1)
            } icon: {
                Image(systemName: "terminal")
            }
            .font(.footnote)
            .foregroundStyle(picked == nil ? Color.orange : SessionStyle.accent)
            .padding(.horizontal, 8)
            .frame(minHeight: 44, alignment: .leading)
        }
        .accessibilityIdentifier("composer-holder-picker")
    }
}
