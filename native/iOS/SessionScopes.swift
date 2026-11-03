import Foundation

extension SessionFilter {
    var pagedScope: String? {
        switch self {
        case .all: return "all"
        case .archived: return "archived"
        case .recent, .running: return nil
        }
    }
}

@MainActor
enum SessionScopes {
    static func readMore(_ filter: SessionFilter, on hosts: [HostConnection]) async {
        guard let scope = filter.pagedScope else { return }
        await withTaskGroup(of: Void.self) { group in
            for host in hosts where host.reachable {
                group.addTask { await host.readMore(scope) }
            }
        }
    }

    static func hasMore(_ filter: SessionFilter, on hosts: [HostConnection]) -> Bool {
        guard let scope = filter.pagedScope else { return false }
        return hosts.contains { $0.reachable && $0.pages.hasMore(scope) }
    }

    static func search(_ query: String, filter: SessionFilter, on hosts: [HostConnection]) async {
        let scope = filter.pagedScope
        await withTaskGroup(of: Void.self) { group in
            for host in hosts {
                group.addTask { await host.search(scope == nil ? "" : query, scope: scope ?? "all") }
            }
        }
    }

    static func error(on hosts: [HostConnection]) -> String? {
        let failing = hosts.filter { $0.reachable && $0.pageError != nil }
        guard !failing.isEmpty else { return nil }
        return failing.count == 1 && hosts.count == 1 ? failing[0].pageError : failing.map { "\($0.name): \($0.pageError ?? "")" }.joined(separator: "\n")
    }
}
