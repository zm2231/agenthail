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
        let cost = name.utf8.count + text.utf8.count + bytes(diffs)
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

    static func bytes(_ diffs: [FileDiff]) -> Int {
        var total = 0
        for diff in diffs {
            total += diff.path.utf8.count
            for hunk in diff.hunks {
                for line in hunk.lines {
                    total += MemoryLayout<TextDiff.Line>.stride + line.text.utf8.count + line.marks.count * MemoryLayout<Range<Int>>.stride
                }
            }
        }
        return total
    }
}
