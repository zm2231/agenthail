import SwiftUI

@MainActor
final class UsageMonitor: ObservableObject {
    @Published private(set) var sets: [UsageReport.Set] = []
    @Published private(set) var problem: String?

    func run(_ model: AgenthailModel) async {
        while !Task.isCancelled {
            await load(model)
            try? await Task.sleep(for: .seconds(120))
        }
    }

    func accounts(now: Date) -> [UsageAccount] {
        UsageAccounts.make(sets, now: now)
    }

    func load(_ model: AgenthailModel) async {
        guard let api = model.api else { return }
        do {
            sets = try await api.usage().sets ?? []
            problem = nil
        } catch AgenthailAPIError.olderHost {
            sets = []
            problem = nil
        } catch {
            if error.isCancellation { return }
            problem = AgenthailAPIError.userMessage(error)
        }
    }
}
