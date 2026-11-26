import Foundation

struct LedgerChange: Decodable, Sendable {
    let projectKey: String
    let version: String
}

enum LedgerReady {
    /// Reads the project's ready count, then again whenever its ledger changes; nil while the ledger can't be read.
    /// Changes are matched against the ledger key the server answered with, not the project id.
    static func follow(api: AgenthailAPI, projectID: String, update: @escaping @MainActor (Int?) -> Void) async {
        while !Task.isCancelled {
            guard let first = try? await api.ledgerReadyCount(projectID: projectID) else {
                await update(nil)
                try? await Task.sleep(for: .seconds(5))
                continue
            }
            await update(first.ready)
            do {
                try await api.streamLedgerChanges { change in
                    guard change.projectKey == first.ledgerKey else { return }
                    let count = try? await api.ledgerReadyCount(projectID: projectID)
                    await update(count?.ready)
                }
            } catch {}
            try? await Task.sleep(for: .seconds(5))
        }
    }
}
