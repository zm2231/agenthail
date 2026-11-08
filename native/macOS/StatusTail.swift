import SwiftUI

struct StatusTail: View {
    var model: AgenthailModel
    var pane: SessionPane
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
            working: shown.contains("working") ? model.workingCount : 0,
            needsYou: shown.contains("needs-you") ? model.needsYouCount : 0,
            hosts: shown.contains("hosts") ? StatusItemsState.hosts(model.daemon?.hosts ?? []) : ""
        )
    }
}
