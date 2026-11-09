#if DEBUG
import Foundation

@MainActor
enum PreviewBackgroundUpserts {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--preview-background-upserts")

    static func run(_ model: AgenthailIOSModel, sparing openID: String) async {
        guard enabled, let host = model.connection("computer") else { return }
        var tick = 0
        while !Task.isCancelled {
            try? await Task.sleep(for: .milliseconds(100))
            tick += 1
            host.updateSnapshot { snapshot in
                guard let index = snapshot.sessions.firstIndex(where: { HostCatalog.qualify("computer", $0.id) != openID }) else { return }
                snapshot.sessions[index].queueCount = tick % 3 + 1
            }
            BodyRuns.hit("upserts")
        }
    }
}
#endif
