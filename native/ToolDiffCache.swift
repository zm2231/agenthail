import os

enum ToolDiffCache {
    private struct Key: Hashable {
        let name: String
        let text: String
    }

    static let limit = 512
    private static let entries = OSAllocatedUnfairLock(initialState: [Key: [FileDiff]]())

    static func diffs(name: String, text: String, make: () -> [FileDiff]) -> [FileDiff] {
        let key = Key(name: name, text: text)
        if let cached = entries.withLock({ $0[key] }) { return cached }
        let diffs = make()
        entries.withLock { entries in
            if entries.count >= limit { entries.removeAll(keepingCapacity: true) }
            entries[key] = diffs
        }
        return diffs
    }
}
