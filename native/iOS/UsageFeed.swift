import SwiftUI

@MainActor
final class UsageFeed: ObservableObject {
    @Published private(set) var accounts: [UsageAccount] = []
    @Published private(set) var now = Date()
    private var sets: [UsageReport.Set] = []
    private var readers: [AgenthailAPI] = []
    private var proxyReaders: [String: Int] = [:]

    func run(_ readers: [AgenthailAPI]) async {
        self.readers = readers
        var minutes = 0
        while !Task.isCancelled {
            if minutes % 2 == 0 {
                let answers = await Self.read(readers)
                if Task.isCancelled { return }
                ingest(answers, at: Date())
            }
            show(at: Date())
            minutes += 1
            try? await Task.sleep(for: .seconds(60))
        }
    }

    func refresh() async {
        ingest(await Self.read(readers), at: Date())
    }

    func ingest(_ answers: [[UsageReport.Set]?], at date: Date) {
        sets = UsageAccounts.merged(answers)
        proxyReaders = UsageProxies.readers(answers)
        show(at: date)
    }

    func show(at date: Date) {
        now = date
        let next = UsageAccounts.make(sets, now: date)
        if next != accounts { accounts = next }
    }

    func api(for account: UsageAccount) -> AgenthailAPI? {
        guard let proxy = account.proxyAccount?.proxyId, let index = proxyReaders[proxy], readers.indices.contains(index) else { return nil }
        return readers[index]
    }

    private static func read(_ readers: [AgenthailAPI]) async -> [[UsageReport.Set]?] {
        await withTaskGroup(of: (Int, [UsageReport.Set]?).self) { group in
            for (index, api) in readers.enumerated() {
                group.addTask { (index, try? await api.usage().sets) }
            }
            var ordered = [[UsageReport.Set]?](repeating: nil, count: readers.count)
            for await (index, answer) in group { ordered[index] = answer }
            return ordered
        }
    }
}
