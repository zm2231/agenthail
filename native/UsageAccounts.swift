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

    var fullTitle: String { local || provider == nil ? login : login + " on " + host }
    var worst: UsageReport.Meter? { meters.max { $0.percent < $1.percent } }
    var critical: Bool { meters.contains { $0.severity == "critical" || $0.percent >= 90 } }

    func spoken(now: Date) -> String {
        let parts = meters.map { UsageMeterText.spokenLabel($0, now: now) } + [problem].compactMap { $0 }
        return fullTitle + ": " + (parts.isEmpty ? "no usage reading" : parts.joined(separator: "; "))
    }

    func subtitle(now: Date) -> String? {
        let parts = [provider == "claude" && rootName != login ? rootName : nil, asOf.map { UsageMeterText.read($0, now: now) }].compactMap { $0 }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    func owns(_ session: SessionState) -> Bool {
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

    static func make(_ sets: [UsageReport.Set], now: Date) -> [UsageAccount] {
        let titles = self.titles(sets)
        return zip(sets, titles).map { set, title in
            UsageAccount(
                id: key(set),
                title: title,
                login: fullName(set),
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
                asOf: set.asOf.flatMap(UsageMeterText.date)
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
        return sets
    }

    static func key(_ set: UsageReport.Set) -> String {
        set.hostId + "|" + (set.provider ?? "") + "|" + (set.root ?? "")
    }

    static func loginLabel(_ login: String?) -> String? {
        switch login {
        case "api_key": return "API key"
        case "signed_out": return "signed out"
        default: return nil
        }
    }

    private static func titles(_ sets: [UsageReport.Set]) -> [String] {
        let names = sets.map(baseName)
        var hostsByName: [String: Swift.Set<String>] = [:]
        for (set, name) in zip(sets, names) where set.provider != nil && loginLabel(set.login) == nil {
            hostsByName[name, default: []].insert(set.hostId)
        }
        let titles = zip(sets, names).map { set, name in
            (hostsByName[name]?.count ?? 0) > 1 ? set.host + " · " + name : name
        }
        var counts: [String: Int] = [:]
        for (set, title) in zip(sets, titles) { counts[set.hostId + "|" + title, default: 0] += 1 }
        return zip(sets, titles).map { set, title in
            guard (counts[set.hostId + "|" + title] ?? 0) > 1, let name = set.name else { return title }
            return title + " · " + name
        }
    }

    private static func baseName(_ set: UsageReport.Set) -> String {
        switch set.provider {
        case nil: return set.host
        case "codex": return "Codex"
        default:
            if let account = set.account, !account.isEmpty {
                return account.contains("@") ? String(account.split(separator: "@")[0]) : account
            }
            if let kind = loginLabel(set.login) { return "\(set.host) (\(kind))" }
            return set.name ?? "Claude"
        }
    }

    private static func fullName(_ set: UsageReport.Set) -> String {
        switch set.provider {
        case nil: return set.host
        case "codex": return "Codex"
        default:
            if let account = set.account, !account.isEmpty { return account }
            if let kind = loginLabel(set.login) { return kind.prefix(1).uppercased() + kind.dropFirst() }
            return set.name ?? "Claude"
        }
    }
}
