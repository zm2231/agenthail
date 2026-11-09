import Foundation

struct ListingState: Equatable {
    var error: String?
    var more: Set<SessionFilter> = []
    var earlier: Set<String> = []
    var foundQueries: [String?] = []

    @MainActor
    static func read(_ hosts: [HostConnection], projects: [String: ListedProject], earlier: (ListedProject) -> [ScopeTarget]) -> ListingState {
        ListingState(
            error: SessionScopes.error(on: hosts),
            more: Set(SessionFilter.allCases.filter { SessionScopes.hasMore(SessionScopes.targets($0, on: hosts)) }),
            earlier: Set(projects.filter { SessionScopes.hasMore(earlier($0.value)) }.keys),
            foundQueries: hosts.filter(\.shownReachable).map(\.reader.foundQuery))
    }

    func searchPending(_ search: String) -> Bool {
        let query = search.trimmingCharacters(in: .whitespacesAndNewlines)
        return !query.isEmpty && foundQueries.contains { $0 != query }
    }
}
