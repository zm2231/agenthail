import SwiftUI

@MainActor
final class UsageFeed: ObservableObject {
    @Published private(set) var sets: [UsageReport.Set] = []

    func run(_ readers: [AgenthailAPI]) async {
        while !Task.isCancelled {
            let answers = await withTaskGroup(of: (Int, [UsageReport.Set]?).self) { group in
                for (index, api) in readers.enumerated() {
                    group.addTask { (index, try? await api.usage().sets) }
                }
                var ordered = [[UsageReport.Set]?](repeating: nil, count: readers.count)
                for await (index, answer) in group { ordered[index] = answer }
                return ordered.compactMap { $0 }
            }
            if Task.isCancelled { return }
            sets = UsageStrip.merged(answers)
            try? await Task.sleep(for: .seconds(120))
        }
    }
}
