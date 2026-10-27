import SwiftUI

struct UsageMetersRow: View {
    @ObservedObject var feed: UsageFeed
    let sessions: [SessionState]
    let open: (String) -> Void
    @State private var showing = false

    var body: some View {
        if !feed.accounts.isEmpty {
            Button { showing = true } label: {
                UsageMetersLine(accounts: feed.accounts, sessions: sessions, now: feed.now)
            }
            .buttonStyle(.plain)
            .accessibilityHint("Shows each account's limits and sessions")
            .accessibilityIdentifier("usage-limits")
            .listRowInsets(EdgeInsets(top: 0, leading: 4, bottom: 4, trailing: 4))
            .listRowSeparator(.hidden)
            .listRowBackground(Color.clear)
            .sheet(isPresented: $showing) {
                UsageAccountsSheet(feed: feed, sessions: sessions) { id in
                    showing = false
                    open(id)
                }
            }
        }
    }
}

struct UsageMetersLine: View {
    let accounts: [UsageAccount]
    var sessions: [SessionState] = []
    let now: Date

    var body: some View {
        let chips = UsageProxies.strip(accounts)
        ViewThatFits(in: .horizontal) {
            row(chips, bars: true, hidden: 0)
            ForEach((1...chips.count).reversed(), id: \.self) { shown in
                row(Array(chips.prefix(shown)), bars: false, hidden: chips.count - shown)
            }
            row([], bars: false, hidden: chips.count)
        }
        .font(.footnote)
        .lineLimit(1)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accounts.map { $0.spoken(now: now) }.joined(separator: ". "))
    }

    private func row(_ shown: [UsageAccount], bars: Bool, hidden: Int) -> some View {
        HStack(spacing: 12) {
            ForEach(shown) { UsageChipLabel(account: $0, sessions: sessions, bars: bars) }
            if hidden > 0 {
                Text(shown.isEmpty ? "\(hidden) accounts" : "+\(hidden)").foregroundStyle(PhonePalette.muted)
            }
        }
        .fixedSize(horizontal: true, vertical: false)
    }
}

extension UsageProblemTone {
    var phoneStyle: RoleStyle {
        switch self {
        case .failed: PhonePalette.failed
        case .warning: PhonePalette.attention
        case .quiet: PhonePalette.muted
        }
    }
}

private struct UsageChipLabel: View {
    let account: UsageAccount
    let sessions: [SessionState]
    let bars: Bool

    var body: some View {
        HStack(spacing: 4) {
            Text(account.title).foregroundStyle(PhonePalette.muted)
            if bars && !account.meters.isEmpty {
                VStack(spacing: 2) {
                    ForEach(account.meters.prefix(3), id: \.self) { PhoneUsageBar(meter: $0).frame(width: 22, height: 3) }
                }
            }
            if let worst = account.worst {
                Text(UsageMeterText.percent(worst.percent))
                    .monospacedDigit()
                    .foregroundStyle(account.critical ? PhonePalette.failed : RoleStyle(.body))
            } else if let tone = account.problemTone(sessions) {
                Image(systemName: "exclamationmark.circle").foregroundStyle(tone.phoneStyle)
            } else {
                Text("–").foregroundStyle(PhonePalette.muted)
            }
        }
    }
}

struct PhoneUsageBar: View {
    let meter: UsageReport.Meter

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .leading) {
                Capsule().fill(PhonePalette.hairline)
                Capsule()
                    .fill(meter.severity == "critical" || meter.percent >= 90 ? PhonePalette.failed : PhonePalette.muted)
                    .frame(width: proxy.size.width * min(max(meter.percent / 100, 0), 1))
            }
        }
    }
}
