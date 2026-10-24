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
                UsageLimits(usage: usage, bars: density == .full)
            }
            Spacer(minLength: density == .none ? 0 : DesktopSpace.l)
            if density != .none {
                ForEach(items.filter { $0 != "usage" }, id: \.self) { item($0) }
            }
        }
        .lineLimit(1)
    }

    private var short: Bool { density == .icons }

    @ViewBuilder
    private func item(_ name: String) -> some View {
        switch name {
        case "working":
            let working = model.knownSessions.filter { $0.archived != true && $0.isWorking }.count
            StatusLabel(title: short ? "\(working)" : "\(working) working", spoken: "\(working) working") {
                Circle().fill(DesktopPalette.working).frame(width: 7, height: 7)
            }
        case "needs-you":
            let needsYou = model.sessionTree.needsYou
            let spoken = "\(needsYou.count) need\(needsYou.count == 1 ? "s" : "") you"
            Button { if let first = needsYou.first { pane.select(first.id) } } label: {
                StatusLabel(title: short ? "\(needsYou.count)" : spoken, spoken: spoken) {
                    Circle().fill(DesktopPalette.attention).frame(width: 7, height: 7)
                }
            }
            .buttonStyle(.plain)
            .disabled(needsYou.isEmpty)
        case "hosts":
            StatusLabel(title: short ? nil : hosts, spoken: hosts) { Image(systemName: "desktopcomputer") }
        case "terminal":
            let session = pane.displayedSession
            Button { if let session { model.focusInTerminal(session) } } label: {
                StatusLabel(title: short ? nil : "Terminal", spoken: "Terminal") { Image(systemName: "terminal") }
            }
            .buttonStyle(.plain)
            .disabled(session?.runtime?.focusable != true)
            .help("Show this session's terminal")
        default:
            EmptyView()
        }
    }

    private var hosts: String {
        let remote = (model.snapshot?.daemon.hosts ?? []).filter { $0.state == "connected" }.map(\.name)
        return (["this Mac"] + remote).joined(separator: " · ")
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

private struct UsageLimits: View {
    @ObservedObject var usage: UsageMonitor
    let bars: Bool

    var body: some View {
        TimelineView(.everyMinute) { context in
            let meters = UsageMeters.meters(provider: "Claude", usage: usage.claude, now: context.date) + UsageMeters.meters(provider: "Codex", usage: usage.codex, now: context.date)
            HStack(spacing: DesktopSpace.l) {
                if meters.isEmpty {
                    Text(usage.problem ?? "Claude")
                }
                ForEach(meters, id: \.label) { meter in
                    HStack(spacing: 6) {
                        Text(meter.label)
                        if bars {
                            ProgressView(value: min(max(meter.percent / 100, 0), 1))
                                .progressViewStyle(.linear)
                                .frame(width: 44)
                                .tint(meter.percent >= 90 ? DesktopPalette.failed : DesktopPalette.text2)
                        }
                        Text(bars ? meter.text : meter.percentText)
                            .monospacedDigit()
                            .foregroundStyle(!bars && meter.percent >= 90 ? DesktopPalette.failed : DesktopPalette.text2)
                    }
                    .help(meter.spokenLabel)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(meter.spokenLabel)
                }
            }
        }
    }
}
