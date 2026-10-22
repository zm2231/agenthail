import SwiftUI

/// Shown only for a Claude conversation open in several terminals: the daemon
/// refuses any write to it that does not name one.
struct HolderPicker: View {
    @ObservedObject var model: AgenthailModel
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
            .pickerStyle(.inline)
        } label: {
            HStack(spacing: DesktopSpace.xs) {
                Image(systemName: "terminal")
                Text(verbatim: picked.map { "pid \(String($0.pid))" } ?? "Choose a terminal")
            }
            .font(.system(size: DesktopType.meta))
            .foregroundStyle(DesktopPalette.accentText)
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .help(SessionFacts.holdersHelp(holders))
        .accessibilityLabel(picked.map { "Sending to terminal \($0.label)" } ?? "Open in \(holders.count) terminals. Choose a terminal")
    }
}
