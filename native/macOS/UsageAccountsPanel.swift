import SwiftUI

struct UsageAccountsPanel: View {
    let accounts: [UsageAccount]
    let sessions: [SessionState]
    let now: Date
    let select: (String) -> Void
    var prefer: ((UsageAccount, ProxyPreferSet) -> Void)? = nil

    var body: some View {
        let hosts = UsageAccounts.byHost(accounts.filter { $0.group == nil })
        let proxies = UsageProxies.byProxy(accounts)
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.l) {
                ForEach(hosts) { host in
                    VStack(alignment: .leading, spacing: DesktopSpace.m) {
                        if hosts.count > 1 || !proxies.isEmpty { UsageGroupHeading(title: host.name, detail: nil) }
                        ForEach(host.accounts) { account in
                            UsageAccountRow(account: account, sessions: account.activeSessions(sessions), now: now, select: select)
                        }
                    }
                }
                ForEach(proxies) { proxy in
                    VStack(alignment: .leading, spacing: DesktopSpace.m) {
                        UsageGroupHeading(title: proxy.group.title, detail: proxy.group.local ? nil : "read from " + proxy.group.host)
                        ForEach(proxy.accounts) { account in
                            UsageAccountRow(account: account, sessions: [], now: now, select: select, prefer: prefer.map { prefer in { prefer(account, $0) } }, only: proxy.hasPeers(account))
                        }
                        if proxy.readable, let problem = proxy.group.problem {
                            Text(problem)
                                .foregroundStyle(UsageProblemTone.warning.desktopStyle)
                                .fixedSize(horizontal: false, vertical: true)
                                .help(proxy.group.problemDetail ?? problem)
                        }
                        if let first = proxy.accounts.first {
                            UsageSessionList(sessions: first.activeSessions(sessions), select: select)
                        }
                    }
                    .font(.system(size: DesktopType.secondary))
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

private struct UsageGroupHeading: View {
    let title: String
    let detail: String?

    var body: some View {
        HStack(spacing: DesktopSpace.s) {
            Text(title).font(.system(size: DesktopType.caption, weight: .semibold))
            if let detail { Text(detail).font(.system(size: DesktopType.caption)) }
        }
        .foregroundStyle(DesktopPalette.muted)
    }
}

private struct UsageAccountRow: View {
    let account: UsageAccount
    let sessions: [SessionState]
    let now: Date
    let select: (String) -> Void
    var prefer: ((ProxyPreferSet) -> Void)? = nil
    var only = false

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
                    Text(account.rowLabel(meter))
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
            if let problem = account.problem, let tone = account.problemTone(sessions) {
                Text(problem)
                    .foregroundStyle(tone.desktopStyle)
                    .fixedSize(horizontal: false, vertical: true)
                    .help(account.problemDetail ?? problem)
            } else if account.meters.isEmpty {
                Text("No current reading").foregroundStyle(DesktopPalette.muted)
            }
            if let prefer, let reference = account.proxyAccount {
                HStack(spacing: DesktopSpace.s) {
                    Toggle(isOn: Binding(get: { reference.preferred }, set: { prefer($0 ? .on : .off) })) {
                        if reference.preferred { Label("Preferred", systemImage: "checkmark") } else { Text("Preferred") }
                    }
                        .toggleStyle(.button)
                        .tint(DesktopPalette.accentText)
                        .help("Changes only this account's preference")
                        .accessibilityIdentifier("proxy-prefer-toggle")
                    if only {
                        Button("Prefer only this") { prefer(.only) }
                            .help("Prefers this account and turns off the other " + (account.provider ?? "") + " accounts on this proxy")
                            .accessibilityIdentifier("proxy-prefer-only")
                    }
                    if let note = reference.setNote { Text(note).foregroundStyle(DesktopPalette.muted) }
                }
                .controlSize(.small)
            }
            UsageSessionList(sessions: sessions, select: select)
        }
        .font(.system(size: DesktopType.secondary))
        .foregroundStyle(DesktopPalette.text2)
        .padding(DesktopSpace.m)
        .background(RoundedRectangle(cornerRadius: 8).fill(DesktopPalette.fill))
        .accessibilityElement(children: .contain)
        .accessibilityLabel(account.fullTitle)
    }
}

private struct UsageSessionList: View {
    let sessions: [SessionState]
    let select: (String) -> Void

    var body: some View {
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
}
