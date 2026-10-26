import SwiftUI

struct UsageAccountsPanel: View {
    let accounts: [UsageAccount]
    let sessions: [SessionState]
    let now: Date
    let select: (String) -> Void

    var body: some View {
        let hosts = UsageAccounts.byHost(accounts)
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.l) {
                ForEach(hosts) { host in
                    VStack(alignment: .leading, spacing: DesktopSpace.m) {
                        if hosts.count > 1 {
                            Text(host.name)
                                .font(.system(size: DesktopType.caption, weight: .semibold))
                                .foregroundStyle(DesktopPalette.muted)
                        }
                        ForEach(host.accounts) { account in
                            UsageAccountRow(account: account, sessions: account.activeSessions(sessions), now: now, select: select)
                        }
                    }
                }
            }
            .padding(DesktopSpace.l)
        }
        .lineLimit(nil)
        .frame(width: 380)
        .frame(maxHeight: 560)
        .fixedSize(horizontal: false, vertical: true)
    }
}

private struct UsageAccountRow: View {
    let account: UsageAccount
    let sessions: [SessionState]
    let now: Date
    let select: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Text(account.login)
                    .font(.system(size: DesktopType.body, weight: .semibold))
                    .foregroundStyle(DesktopPalette.text)
                Spacer(minLength: DesktopSpace.s)
                if let subtitle = account.subtitle(now: now) {
                    Text(subtitle).foregroundStyle(DesktopPalette.muted)
                }
            }
            ForEach(account.meters, id: \.self) { meter in
                HStack(spacing: DesktopSpace.s) {
                    Text(UsageMeterText.shortLabel(meter))
                        .frame(width: 78, alignment: .leading)
                    UsageBar(meter: meter).frame(height: 4)
                    Text(UsageMeterText.percent(meter.percent))
                        .monospacedDigit()
                        .frame(width: 36, alignment: .trailing)
                        .foregroundStyle(meter.severity == "critical" || meter.percent >= 90 ? DesktopPalette.failed : DesktopPalette.text)
                    Text(meter.resetsAt.flatMap(UsageMeterText.date).map { UsageMeterText.reset(until: $0, now: now) } ?? "")
                        .monospacedDigit()
                        .frame(width: 92, alignment: .trailing)
                        .foregroundStyle(DesktopPalette.muted)
                }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(UsageMeterText.spokenLabel(meter, now: now))
            }
            if let problem = account.problem {
                Text(problem)
                    .foregroundStyle(account.meters.isEmpty ? DesktopPalette.failed : DesktopPalette.muted)
                    .fixedSize(horizontal: false, vertical: true)
            } else if account.meters.isEmpty {
                Text("No current reading").foregroundStyle(DesktopPalette.muted)
            }
            if !sessions.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(sessions.prefix(5)) { session in
                        Button { select(session.id) } label: {
                            HStack(spacing: 6) {
                                Circle()
                                    .fill(session.isWorking ? DesktopPalette.working : DesktopPalette.muted)
                                    .frame(width: 6, height: 6)
                                Text(session.displayName).lineLimit(1).truncationMode(.tail)
                            }
                        }
                        .buttonStyle(ReadableButtonStyle())
                        .accessibilityLabel("\(session.displayName), \(session.isWorking ? "working" : "open")")
                    }
                    if sessions.count > 5 {
                        Text("and \(sessions.count - 5) more").foregroundStyle(DesktopPalette.muted)
                    }
                }
                .padding(.top, 2)
            }
        }
        .font(.system(size: DesktopType.secondary))
        .foregroundStyle(DesktopPalette.text2)
        .padding(DesktopSpace.m)
        .background(RoundedRectangle(cornerRadius: 8).fill(DesktopPalette.fill))
        .accessibilityElement(children: .contain)
        .accessibilityLabel(account.fullTitle)
    }
}
