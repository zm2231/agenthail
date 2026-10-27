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
        var best: [String: Int] = [:]
        for (index, set) in sets.enumerated() where set.provider == "proxy" {
            guard let id = set.proxy?.id else { continue }
            if let current = best[id], sets[current].proxyProblem == nil || set.proxyProblem != nil { continue }
            best[id] = index
        }
        var placed = Swift.Set<String>()
        return sets.enumerated().compactMap { index, set in
            guard set.provider == "proxy", let id = set.proxy?.id else { return set }
            guard placed.insert(id).inserted else { return nil }
            return sets[best[id] ?? index]
        }
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
