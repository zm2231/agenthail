import SwiftUI

struct UsageChips: View {
    @ObservedObject var usage: UsageMonitor
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let bars: Bool
    @State private var showing = false

    var body: some View {
        TimelineView(.everyMinute) { context in
            let accounts = usage.accounts(now: context.date)
            if accounts.isEmpty {
                if let problem = usage.problem { Text(problem).help(problem) }
            } else {
                Button { showing.toggle() } label: {
                    HStack(spacing: DesktopSpace.m) {
                        ForEach(accounts) { UsageChip(account: $0, sessions: model.knownSessions, bars: bars, now: context.date) }
                    }
                }
                .buttonStyle(ReadableButtonStyle())
                .help("Usage of every account")
                .accessibilityLabel(accounts.map { $0.spoken(now: context.date) }.joined(separator: ". "))
                .accessibilityHint("Shows each account's limits and sessions")
                .accessibilityIdentifier("usage-accounts")
                .popover(isPresented: $showing, arrowEdge: .top) {
                    UsageAccountsPanel(accounts: accounts, sessions: model.knownSessions, now: context.date) { id in
                        showing = false
                        pane.select(id)
                    }
                }
            }
        }
    }
}

extension UsageProblemTone {
    var desktopStyle: DesktopStyle {
        switch self {
        case .failed: DesktopPalette.failed
        case .warning: DesktopPalette.attention
        case .quiet: DesktopPalette.text2
        }
    }
}

private struct UsageChip: View {
    let account: UsageAccount
    let sessions: [SessionState]
    let bars: Bool
    let now: Date

    var body: some View {
        HStack(spacing: 5) {
            Text(account.title)
            if bars && !account.meters.isEmpty {
                UsageMiniBars(meters: account.meters)
            }
            value
        }
    }

    @ViewBuilder
    private var value: some View {
        if let worst = account.worst {
            Text(UsageMeterText.percent(worst.percent))
                .monospacedDigit()
                .foregroundStyle(account.critical ? DesktopPalette.failed : DesktopPalette.text2)
        } else if let tone = account.problemTone(sessions) {
            Image(systemName: "exclamationmark.circle").foregroundStyle(tone == .quiet ? DesktopPalette.muted : tone.desktopStyle)
        } else {
            Text("–").foregroundStyle(DesktopPalette.muted)
        }
    }
}

struct UsageMiniBars: View {
    let meters: [UsageReport.Meter]

    var body: some View {
        VStack(spacing: 2) {
            ForEach(meters.prefix(3), id: \.self) { meter in
                UsageBar(meter: meter).frame(width: 26, height: 3)
            }
        }
    }
}

struct UsageBar: View {
    let meter: UsageReport.Meter

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .leading) {
                Capsule().fill(DesktopPalette.line)
                Capsule()
                    .fill(meter.severity == "critical" || meter.percent >= 90 ? DesktopPalette.failed : DesktopPalette.text2)
                    .frame(width: proxy.size.width * min(max(meter.percent / 100, 0), 1))
            }
        }
    }
}
