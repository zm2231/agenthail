import SwiftUI

struct StatusTail: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @ObservedObject var usage: UsageMonitor
    let items: [String]
    let density: StatusDensity

    var body: some View {
        HStack(spacing: DesktopSpace.l) {
            if items.contains("usage") && (density == .full || density == .compact) {
                UsageChips(usage: usage, model: model, pane: pane, bars: density == .full)
            }
            Spacer(minLength: density == .none ? 0 : DesktopSpace.l)
            if density != .none {
                StatusItems(state: itemsState, model: model, pane: pane)
                    .equatable()
            }
        }
        .lineLimit(1)
    }

    private var itemsState: StatusItemsState {
        let shown = items.filter { $0 != "usage" }
        return StatusItemsState(
            items: shown,
            short: density == .icons,
            working: shown.contains("working") ? model.knownSessions.filter { $0.archived != true && SessionDisplayKind.of($0) == .working }.count : 0,
            needsYou: shown.contains("needs-you") ? model.sessionTree.needsYou.count : 0,
            hosts: shown.contains("hosts") ? StatusItemsState.hosts(model.snapshot?.daemon.hosts ?? []) : "",
            terminalFocusable: shown.contains("terminal") && pane.displayedSession?.runtime?.focusable == true
        )
    }
}

private struct StatusItems: View, @MainActor Equatable {
    let state: StatusItemsState
    let model: AgenthailModel
    let pane: SessionPane

    static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.state == rhs.state && lhs.model === rhs.model && lhs.pane === rhs.pane
    }

    var body: some View {
        ForEach(state.items, id: \.self) { item($0) }
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
            Button { if let session = pane.displayedSession { model.focusInTerminal(session) } } label: {
                StatusLabel(title: state.short ? nil : "Terminal", spoken: "Terminal") { Image(systemName: "terminal") }
            }
            .buttonStyle(ReadableButtonStyle())
            .disabled(!state.terminalFocusable)
            .help("Show this session's terminal")
        default:
            EmptyView()
        }
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
