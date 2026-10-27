import SwiftUI

struct UsageAccountsSheet: View {
    @ObservedObject var feed: UsageFeed
    let sessions: [SessionState]
    let open: (String) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                ForEach(UsageAccounts.byHost(feed.accounts.filter { $0.group == nil })) { host in
                    Section(host.name) {
                        ForEach(host.accounts) { account in
                            PhoneUsageAccountRow(account: account, sessions: account.activeSessions(sessions), now: feed.now, open: open)
                        }
                    }
                }
                ForEach(UsageProxies.byProxy(feed.accounts)) { proxy in
                    Section {
                        ForEach(proxy.accounts) { account in
                            PhoneUsageAccountRow(account: account, sessions: [], now: feed.now, open: open)
                        }
                        if proxy.readable, let problem = proxy.group.problem {
                            Text(problem).font(.footnote).foregroundStyle(UsageProblemTone.warning.phoneStyle)
                                .accessibilityHint(proxy.group.problemDetail ?? "")
                        }
                        if let first = proxy.accounts.first {
                            PhoneUsageSessions(sessions: first.activeSessions(sessions), open: open)
                        }
                    } header: {
                        Text(proxy.group.local ? proxy.group.title : proxy.group.title + " · read from " + proxy.group.host)
                    }
                }
            }
            .navigationTitle("Accounts")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
        .presentationDetents([.medium, .large])
        .accessibilityIdentifier("usage-accounts")
    }
}

private struct PhoneUsageAccountRow: View {
    let account: UsageAccount
    let sessions: [SessionState]
    let now: Date
    let open: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text(account.login).font(.headline)
                if let subtitle = account.subtitle(now: now) { Text(subtitle).font(.footnote).foregroundStyle(PhonePalette.muted) }
            }
            ForEach(account.meters, id: \.self) { meter in
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Text(account.rowLabel(meter))
                        Spacer()
                        Text(UsageMeterText.percent(meter.percent))
                            .monospacedDigit()
                            .foregroundStyle(meter.severity == "critical" || meter.percent >= 90 ? PhonePalette.failed : RoleStyle(.body))
                        if let reset = meter.resetsAt.flatMap(UsageMeterText.date) {
                            Text(UsageMeterText.reset(until: reset, now: now)).monospacedDigit().foregroundStyle(PhonePalette.muted)
                        }
                    }
                    .font(.subheadline)
                    PhoneUsageBar(meter: meter).frame(height: 4)
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(UsageMeterText.spokenLabel(meter, now: now))
            }
            if let problem = account.problem, let tone = account.problemTone(sessions) {
                Text(problem).font(.footnote).foregroundStyle(tone.phoneStyle)
                    .accessibilityHint(account.problemDetail ?? "")
            } else if account.meters.isEmpty {
                Text("No current reading").font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            PhoneUsageSessions(sessions: sessions, open: open)
        }
        .padding(.vertical, 4)
    }
}

private struct PhoneUsageSessions: View {
    let sessions: [SessionState]
    let open: (String) -> Void

    var body: some View {
        ForEach(sessions.prefix(5)) { session in
            Button { open(session.id) } label: {
                HStack(spacing: 8) {
                    Circle().fill(session.isWorking ? PhonePalette.working : PhonePalette.muted).frame(width: 7, height: 7)
                    Text(session.displayName).lineLimit(1)
                }
                .font(.subheadline)
                .foregroundStyle(RoleStyle(.body))
            }
            .buttonStyle(.borderless)
        }
        if sessions.count > 5 {
            Text("and \(sessions.count - 5) more").font(.footnote).foregroundStyle(PhonePalette.muted)
        }
    }
}
