import os

enum ToolDiffCache {
    private struct Key: Hashable {
        let name: String
        let text: String
    }

    private struct State {
        var entries: [Key: [FileDiff]] = [:]
        var bytes = 0
    }

    static let limit = 512
    static let byteBudget = 4 << 20
    private static let state = OSAllocatedUnfairLock(initialState: State())

    static var retainedBytes: Int { state.withLock { $0.bytes } }

    static func diffs(name: String, text: String, make: () -> [FileDiff]) -> [FileDiff] {
        let key = Key(name: name, text: text)
        if let cached = state.withLock({ $0.entries[key] }) { return cached }
        let diffs = make()
        let cost = 2 * (name.utf8.count + text.utf8.count)
        guard cost <= byteBudget else { return diffs }
        state.withLock { state in
            guard state.entries[key] == nil else { return }
            if state.entries.count >= limit || state.bytes + cost > byteBudget {
                state.entries.removeAll(keepingCapacity: true)
                state.bytes = 0
            }
            state.entries[key] = diffs
            state.bytes += cost
        }
        return diffs
    }
}
