import SwiftUI

@MainActor
final class UsageFeed: ObservableObject {
    @Published private(set) var accounts: [UsageAccount] = []
    @Published private(set) var now = Date()
    private var sets: [UsageReport.Set] = []

    func run(_ readers: [AgenthailAPI]) async {
        var minutes = 0
        while !Task.isCancelled {
            if minutes % 2 == 0 {
                let answers = await Self.read(readers)
                if Task.isCancelled { return }
                sets = UsageAccounts.merged(answers)
            }
            show(at: Date())
            minutes += 1
            try? await Task.sleep(for: .seconds(60))
        }
    }

    func ingest(_ answers: [[UsageReport.Set]?], at date: Date) {
        sets = UsageAccounts.merged(answers)
        show(at: date)
    }

    func show(at date: Date) {
        now = date
        let next = UsageAccounts.make(sets, now: date)
        if next != accounts { accounts = next }
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
