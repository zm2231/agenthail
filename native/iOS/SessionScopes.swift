import Foundation

struct ScopeTarget {
    let host: HostConnection
    let request: ScopeRequest
}

@MainActor
enum SessionScopes {
    static func targets(_ request: ScopeRequest?, on hosts: [HostConnection]) -> [ScopeTarget] {
        guard let request else { return [] }
        return hosts.map { ScopeTarget(host: $0, request: request) }
    }

    static func readMore(_ targets: [ScopeTarget]) async {
        await withTaskGroup(of: Void.self) { group in
            for target in targets where target.host.reachable {
                group.addTask { await target.host.readMore(target.request) }
            }
        }
    }

    static func hasMore(_ targets: [ScopeTarget]) -> Bool {
        targets.contains { $0.host.reachable && $0.host.pages.hasMore($0.request) }
    }

    static func search(_ query: String, in request: ScopeRequest, on hosts: [HostConnection]) async {
        await withTaskGroup(of: Void.self) { group in
            for host in hosts {
                group.addTask { await host.search(query, in: request) }
            }
        }
    }

    static func error(on hosts: [HostConnection]) -> String? {
        let failing = hosts.filter { $0.reachable && $0.pageError != nil }
        guard !failing.isEmpty else { return nil }
        return failing.count == 1 && hosts.count == 1 ? failing[0].pageError : failing.map { "\($0.name): \($0.pageError ?? "")" }.joined(separator: "\n")
    }
}
