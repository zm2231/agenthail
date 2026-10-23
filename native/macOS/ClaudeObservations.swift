import SwiftUI

struct ClaudeObservationsSection: View {
    let detail: SessionDetail?

    var body: some View {
        if let runs = detail?.claudeRuns, !runs.isEmpty {
            group("Claude runs") {
                ForEach(runs) { run in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(run.jobId)
                        let facts = [run.providerState, run.runType, run.updatedAt.map { "Updated \($0)" }].compactMap { $0 }.filter { !$0.isEmpty }
                        if !facts.isEmpty {
                            Text(facts.joined(separator: " · "))
                                .font(.system(size: DesktopType.meta))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                    }
                }
            }
        }
        if let errors = detail?.metadataErrors, !errors.isEmpty {
            group("Not available right now") {
                Text(Self.unavailable(errors.keys))
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.text2)
                    .textSelection(.enabled)
            }
        }
    }

    static let fieldNames = [
        "context": "Context usage", "goal": "Goal", "model": "Current model", "pendingModel": "Next model",
        "models": "Model list", "claudeRuns": "Claude runs", "claudeSubagents": "Subagents",
    ]

    static func unavailable(_ keys: some Collection<String>) -> String {
        keys.sorted().map { fieldNames[$0] ?? $0 }.joined(separator: ", ")
    }

    private func group<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            SidebarCaption(title).padding(.horizontal, -8)
            content()
        }
    }
}
