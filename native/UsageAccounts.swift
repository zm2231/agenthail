import Foundation

enum UsageProblemTone {
    case quiet, warning, failed
}

struct UsageAccount: Equatable, Identifiable {
    let id: String
    let title: String
    let login: String
    let signIn: String?
    let host: String
    let hostId: String
    let local: Bool
    let provider: String?
    let rootName: String?
    let root: String?
    let meters: [UsageReport.Meter]
    let problem: String?
    let problemDetail: String?
    let asOf: Date?
    var group: UsageProxyGroup? = nil

    var fullTitle: String {
        if let group, provider != "proxy" { return login + " via the " + group.name + " proxy" }
        return local || provider == nil ? login : login + " on " + host
    }
    var worst: UsageReport.Meter? { meters.max { $0.percent < $1.percent } }
    var caps: [Double] { Array(Swift.Set(meters.compactMap(\.ceiling))).sorted() }

    func rowLabel(_ meter: UsageReport.Meter) -> String {
        guard caps.count > 1, let ceiling = meter.ceiling else { return UsageMeterText.shortLabel(meter) }
        return UsageMeterText.shortLabel(meter) + ", cap " + UsageMeterText.percent(ceiling)
    }
    var critical: Bool { meters.contains { $0.severity == "critical" || $0.percent >= 90 } }

    func spoken(now: Date) -> String {
        let parts = meters.map { UsageMeterText.spokenLabel($0, now: now) } + [problem].compactMap { $0 }
        return fullTitle + ": " + (parts.isEmpty ? "no usage reading" : parts.joined(separator: "; "))
    }

    func subtitle(now: Date) -> String? {
        let cap = caps.count == 1 ? caps.first.map { "cap " + UsageMeterText.percent($0) } : nil
        let over = meters.contains { meter in meter.ceiling.map { $0 > 0 && meter.percent >= $0 } ?? false } ? "over cap" : nil
        let parts = [provider == "claude" && group == nil && rootName != login ? rootName : nil, cap, over, asOf.map { UsageMeterText.read($0, now: now) }].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    func owns(_ session: SessionState) -> Bool {
        if let group {
            guard session.archived != true, session.surface == "claude" else { return false }
            return group.roots.contains { $0.hostId == session.host?.id && $0.root == session.claudeConfigRoot?.root }
        }
        guard session.archived != true, session.host?.id == hostId else { return false }
        switch provider {
        case "claude": return session.surface == "claude" && session.claudeConfigRoot?.root == root
        case "codex": return session.surface == "codex"
        default: return false
        }
    }

    func activeSessions(_ sessions: [SessionState]) -> [SessionState] {
        sessions.filter { owns($0) && ($0.isWorking || $0.current) }
            .sorted { ($0.isWorking ? 0 : 1, $1.lastActive ?? "") < ($1.isWorking ? 0 : 1, $0.lastActive ?? "") }
    }

    func problemTone(_ sessions: [SessionState]) -> UsageProblemTone? {
        guard problem != nil else { return nil }
        if provider == "proxy" { return .warning }
        if group != nil { return .quiet }
        switch signIn {
        case "api_key": return .quiet
        case "signed_out": return activeSessions(sessions).isEmpty ? .quiet : .warning
        default: return meters.isEmpty ? .failed : .quiet
        }
    }
}

struct UsageHostGroup: Identifiable {
    let id: String
    let name: String
    let accounts: [UsageAccount]
}

enum UsageAccounts {
    static func byHost(_ accounts: [UsageAccount]) -> [UsageHostGroup] {
        var order: [String] = []
        var grouped: [String: [UsageAccount]] = [:]
        for account in accounts {
            if grouped[account.hostId] == nil { order.append(account.hostId) }
            grouped[account.hostId, default: []].append(account)
        }
        return order.map { id in UsageHostGroup(id: id, name: grouped[id]?.first?.host ?? id, accounts: grouped[id] ?? []) }
    }

    static func make(_ reported: [UsageReport.Set], now: Date) -> [UsageAccount] {
        let expanded = UsageProxies.expand(reported, now: now)
        let titles = self.titles(expanded)
        return zip(expanded, titles).map { entry, title in
            let (set, group) = entry
            return UsageAccount(
                id: key(set),
                title: title,
                login: fullName(set, group: group),
                signIn: set.login,
                host: set.host,
                hostId: set.hostId,
                local: set.local == true,
                provider: set.provider,
                rootName: set.name,
                root: set.root,
                meters: UsageMeterText.visible(set.meters ?? [], now: now),
                problem: set.problem.flatMap { $0.isEmpty ? nil : $0 },
                problemDetail: set.problemDetail.flatMap { $0.isEmpty ? nil : $0 },
                asOf: set.asOf.flatMap(UsageMeterText.date),
                group: group
            )
        }
    }

    static func merged(_ answers: [[UsageReport.Set]?]) -> [UsageReport.Set] {
        var seen = Swift.Set<String>()
        var sets: [UsageReport.Set] = []
        for (index, answer) in answers.enumerated() {
            for var set in answer ?? [] where seen.insert(key(set)).inserted {
                if index > 0 { set.local = false }
                sets.append(set)
            }
        }
        return UsageProxies.merged(sets)
    }

    static func key(_ set: UsageReport.Set) -> String {
        set.hostId + "|" + (set.provider ?? "") + "|" + (set.root ?? set.proxy?.id ?? "")
    }

    static func loginLabel(_ login: String?) -> String? {
        switch login {
        case "api_key": return "API key"
        case "signed_out": return "signed out"
        default: return nil
        }
    }

    private static func titles(_ entries: [(UsageReport.Set, UsageProxyGroup?)]) -> [String] {
        let names = entries.map { baseName($0.0, group: $0.1) }
        var hostsByName: [String: Swift.Set<String>] = [:]
        for ((set, group), name) in zip(entries, names) where set.provider != nil && group == nil && loginLabel(set.login) == nil {
            hostsByName[name, default: []].insert(set.hostId)
        }
        let titles = zip(entries, names).map { entry, name in
            entry.1 == nil && (hostsByName[name]?.count ?? 0) > 1 ? entry.0.host + " · " + name : name
        }
        var counts: [String: Int] = [:]
        for (entry, title) in zip(entries, titles) where entry.1 == nil { counts[entry.0.hostId + "|" + title, default: 0] += 1 }
        return zip(entries, titles).enumerated().map { index, pair in
            let ((set, group), title) = pair
            if let group {
                let repeated = set.provider != "proxy" && names.enumerated().contains { $0.offset != index && $0.element == names[index] }
                return repeated ? title + " · " + group.name : title
            }
            guard (counts[set.hostId + "|" + title] ?? 0) > 1, let name = set.name else { return title }
            return title + " · " + name
        }
    }

    private static func baseName(_ set: UsageReport.Set, group: UsageProxyGroup?) -> String {
        if let group, set.provider == "proxy" { return group.title }
        let codex = group != nil && set.provider == "codex" ? " (Codex)" : ""
        switch set.provider {
        case nil: return set.host
        case "codex" where group == nil: return "Codex"
        default:
            if let account = set.account, !account.isEmpty {
                return (account.contains("@") ? String(account.split(separator: "@")[0]) : account) + codex
            }
            if let kind = loginLabel(set.login) { return "\(set.host) (\(kind))" }
            return set.name ?? "Claude"
        }
    }

    private static func fullName(_ set: UsageReport.Set, group: UsageProxyGroup?) -> String {
        if let group, set.provider == "proxy" { return group.title }
        let codex = group != nil && set.provider == "codex" ? " (Codex)" : ""
        switch set.provider {
        case nil: return set.host
        case "codex" where group == nil: return "Codex"
        default:
            if let account = set.account, !account.isEmpty { return account + codex }
            if let kind = loginLabel(set.login) { return kind.prefix(1).uppercased() + kind.dropFirst() }
            return set.name ?? "Claude"
        }
    }
}
