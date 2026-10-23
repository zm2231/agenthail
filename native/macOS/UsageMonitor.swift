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

    func strip(now: Date) -> UsageStrip {
        var strip = UsageStrip.make(sets, now: now)
        if strip.meters.isEmpty && strip.problem == nil { strip.problem = problem }
        return strip
    }

    private func load(_ model: AgenthailModel) async {
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
