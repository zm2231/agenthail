import Foundation

struct UsageProxyGroup: Equatable, Hashable, Identifiable {
    struct Root: Equatable, Hashable {
        let hostId: String
        let root: String?
    }
    let id: String
    let name: String
    let host: String
    let local: Bool
    let problem: String?
    let problemDetail: String?
    let roots: [Root]

    var title: String { name + " (proxy)" }
}

struct UsageProxyGroupView: Identifiable {
    let group: UsageProxyGroup
    let accounts: [UsageAccount]
    var id: String { group.id }
    var readable: Bool { accounts.contains { $0.provider != "proxy" } }

    func hasPeers(_ account: UsageAccount) -> Bool {
        accounts.filter { $0.provider == account.provider && $0.problem != "disabled" }.count > 1
    }
}

enum UsageProxies {
    static func group(_ set: UsageReport.Set, in sets: [UsageReport.Set]) -> UsageProxyGroup {
        let id = set.proxy?.id ?? set.hostId + "|" + (set.name ?? "")
        let roots = sets.filter { $0.provider == "claude" && $0.proxy != nil && $0.proxy?.id == set.proxy?.id }
            .map { UsageProxyGroup.Root(hostId: $0.hostId, root: $0.root) }
        return UsageProxyGroup(id: id, name: set.name ?? "proxy", host: set.host, local: set.local == true, problem: set.problem, problemDetail: set.problemDetail, roots: roots)
    }

    static func expand(_ sets: [UsageReport.Set], now: Date) -> [(UsageReport.Set, UsageProxyGroup?)] {
        var expanded: [(UsageReport.Set, UsageProxyGroup?)] = []
        for set in sets {
            if set.provider == "claude", set.proxy != nil { continue }
            guard set.provider == "proxy" else {
                expanded.append((set, nil))
                continue
            }
            let group = self.group(set, in: sets)
            let accounts = set.accounts ?? []
            if accounts.isEmpty {
                expanded.append((UsageReport.Set(host: set.host, hostId: set.hostId, local: set.local, provider: "proxy", name: set.name, root: "proxy:" + group.id, meters: [], problem: set.problem, problemDetail: set.problemDetail), group))
            }
            for account in accounts {
                let entry = UsageReport.Set(host: set.host, hostId: set.hostId, local: set.local, provider: account.provider, name: set.name, account: account.label, root: "proxy:" + group.id + ":" + account.id, meters: account.meters ?? [], asOf: account.asOf, problem: state(account, now: now))
                expanded.append((entry, group))
            }
        }
        return expanded
    }

    static func references(_ sets: [UsageReport.Set]) -> [String: UsageProxyAccountRef] {
        var references: [String: UsageProxyAccountRef] = [:]
        for set in sets where set.provider == "proxy" {
            guard let proxy = set.proxy?.id else { continue }
            for account in set.accounts ?? [] {
                references["proxy:" + proxy + ":" + account.id] = account.reference(proxy: proxy)
            }
        }
        return references
    }

    // The computer that reported a readable proxy routes a change to the host holding its key.
    static func readers(_ answers: [[UsageReport.Set]?]) -> [String: Int] {
        var readers: [String: Int] = [:]
        for (index, answer) in answers.enumerated() {
            for set in answer ?? [] where set.provider == "proxy" && set.proxyProblem == nil && !(set.accounts ?? []).isEmpty {
                if let id = set.proxy?.id, readers[id] == nil { readers[id] = index }
            }
        }
        return readers
    }

    static func state(_ account: UsageReport.ProxyAccount, now: Date) -> String? {
        switch account.status {
        case "cooling":
            guard let retry = account.nextRetry.flatMap(UsageMeterText.date), retry > now else { return "cooling" }
            return "cooling for " + wait(retry.timeIntervalSince(now))
        case "disabled", "unavailable": return account.status
        default: return nil
        }
    }

    static func wait(_ seconds: TimeInterval) -> String {
        let minutes = max(Int((seconds / 60).rounded()), 1)
        let hours = minutes / 60
        if hours >= 24 { return "\(hours / 24)d \(hours % 24)h" }
        return hours > 0 ? "\(hours)h \(minutes % 60)m" : "\(minutes)m"
    }

    // Each paired computer reports a shared proxy; keep one, read from a host whose key works when any does.
    static func merged(_ sets: [UsageReport.Set]) -> [UsageReport.Set] {
        func rank(_ set: UsageReport.Set) -> Int {
            switch set.proxyProblem {
            case nil: 0
            case "key_missing": 2
            default: 1
            }
        }
        var best: [String: Int] = [:]
        for (index, set) in sets.enumerated() where set.provider == "proxy" {
            guard let id = set.proxy?.id else { continue }
            if let current = best[id], rank(sets[current]) <= rank(set) { continue }
            best[id] = index
        }
        var placed = Swift.Set<String>()
        return sets.enumerated().compactMap { index, set in
            guard set.provider == "proxy", let id = set.proxy?.id else { return set }
            guard placed.insert(id).inserted else { return nil }
            return sets[best[id] ?? index]
        }
    }

    static func strip(_ accounts: [UsageAccount]) -> [UsageAccount] {
        let groups = byProxy(accounts)
        var placed = Swift.Set<String>()
        return accounts.compactMap { account in
            guard let group = account.group else { return account }
            guard placed.insert(group.id).inserted, let view = groups.first(where: { $0.id == group.id }) else { return nil }
            return chip(view)
        }
    }

    private static func worse(_ a: UsageReport.Meter, _ b: UsageReport.Meter) -> Bool {
        let rank = { (meter: UsageReport.Meter) in (meter.severity == "critical" || meter.percent >= 90 ? 1 : 0, meter.percent) }
        return rank(a) > rank(b)
    }

    private static func chip(_ view: UsageProxyGroupView) -> UsageAccount {
        let rated = view.accounts.compactMap { account in account.meters.max { worse($1, $0) }.map { (account, $0) } }
        let top = rated.max { worse($1.1, $0.1) }
        let lead = top?.0 ?? view.accounts[0]
        let suffix = " · " + view.group.name
        let name = lead.title.hasSuffix(suffix) ? String(lead.title.dropLast(suffix.count)) : lead.title
        let title = lead.provider == "proxy" ? lead.title : view.group.name + " · " + name
        return UsageAccount(id: "proxy:" + view.group.id, title: title, login: lead.login, signIn: lead.signIn, host: lead.host, hostId: lead.hostId, local: lead.local, provider: lead.provider, rootName: lead.rootName, root: lead.root, meters: top.map { [$0.1] } ?? [], problem: top == nil ? lead.problem : nil, problemDetail: top == nil ? lead.problemDetail : nil, asOf: lead.asOf, group: lead.group)
    }

    static func byProxy(_ accounts: [UsageAccount]) -> [UsageProxyGroupView] {
        var order: [String] = []
        var grouped: [String: (UsageProxyGroup, [UsageAccount])] = [:]
        for account in accounts {
            guard let group = account.group else { continue }
            if grouped[group.id] == nil { order.append(group.id) }
            grouped[group.id, default: (group, [])].1.append(account)
        }
        return order.compactMap { grouped[$0].map { UsageProxyGroupView(group: $0.0, accounts: $0.1) } }
    }
}
