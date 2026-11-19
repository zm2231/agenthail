import Foundation

struct LedgerChange: Decodable, Sendable {
    let projectKey: String
    let version: String
}

enum LedgerReady {
    /// Reads the project's ready count, then again whenever its ledger changes; nil while the ledger can't be read.
    static func follow(api: AgenthailAPI, projectKey: String, update: @escaping @MainActor (Int?) -> Void) async {
        while !Task.isCancelled {
            await update(try? await api.ledgerReadyCount(projectKey: projectKey))
            do {
                try await api.streamLedgerChanges { change in
                    guard change.projectKey == projectKey else { return }
                    let count = try? await api.ledgerReadyCount(projectKey: projectKey)
                    await update(count)
                }
            } catch {}
            try? await Task.sleep(for: .seconds(5))
        }
    }
}
