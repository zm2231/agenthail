import SwiftUI

struct StatusItems: View, @MainActor Equatable {
    let state: StatusItemsState
    let model: AgenthailModel
    let pane: SessionPane

    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.state == rhs.state && lhs.model === rhs.model && lhs.pane === rhs.pane
    }

    var body: some View {
        HStack(spacing: DesktopSpace.l) {
            ForEach(state.items, id: \.self) { item($0) }
        }
    }

    @ViewBuilder
    private func item(_ name: String) -> some View {
        switch name {
        case "working":
            StatusLabel(title: state.short ? "\(state.working)" : "\(state.working) working", spoken: "\(state.working) working") {
                Circle().fill(DesktopPalette.working).frame(width: 7, height: 7)
            }
        case "needs-you":
            let spoken = "\(state.needsYou) need\(state.needsYou == 1 ? "s" : "") you"
            Button { if let first = model.sessionTree.needsYou.first { pane.select(first.id) } } label: {
                StatusLabel(title: state.short ? "\(state.needsYou)" : spoken, spoken: spoken) {
                    Circle().fill(DesktopPalette.attention).frame(width: 7, height: 7)
                }
            }
            .buttonStyle(ReadableButtonStyle())
            .disabled(state.needsYou == 0)
        case "hosts":
            StatusLabel(title: state.short ? nil : state.hosts, spoken: state.hosts) { Image(systemName: "desktopcomputer") }
        case "terminal":
            TerminalStatusItem(model: model, pane: pane, short: state.short)
        default:
            EmptyView()
        }
    }
}

private struct TerminalStatusItem: View {
    let model: AgenthailModel
    let pane: SessionPane
    let short: Bool

    var body: some View {
        let session = pane.displayedSession
        Button { if let session { model.focusInTerminal(session) } } label: {
            StatusLabel(title: short ? nil : "Terminal", spoken: "Terminal") { Image(systemName: "terminal") }
        }
        .buttonStyle(ReadableButtonStyle())
        .disabled(session?.runtime?.focusable != true)
        .help("Show this session's terminal")
    }
}

private struct StatusLabel<Icon: View>: View {
    let title: String?
    let spoken: String
    @ViewBuilder let icon: Icon

    var body: some View {
        HStack(spacing: 5) {
            icon
            if let title { Text(title) }
        }
        .help(title == spoken ? "" : spoken)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(spoken)
    }
}
