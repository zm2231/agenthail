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
                                .font(.system(size: 11.5))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                    }
                }
            }
        }
        if let links = detail?.claudeSubagents, !links.isEmpty {
            group("Claude subagents") {
                ForEach(links) { link in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(link.agentId)
                        if !link.transcriptPath.isEmpty {
                            Text((link.transcriptPath as NSString).abbreviatingWithTildeInPath)
                                .font(.system(size: 11, design: .monospaced))
                                .foregroundStyle(DesktopPalette.text2)
                                .lineLimit(1)
                                .truncationMode(.middle)
                                .textSelection(.enabled)
                        }
                    }
                }
            }
        }
        if let errors = detail?.metadataErrors, !errors.isEmpty {
            group("Some details couldn't load") {
                ForEach(errors.keys.sorted(), id: \.self) { key in
                    if let message = errors[key] {
                        Text("\(key): \(message)")
                            .font(.system(size: 11.5))
                            .foregroundStyle(DesktopPalette.amber)
                            .textSelection(.enabled)
                    }
                }
            }
        }
    }

    private func group<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            SidebarCaption(title).padding(.horizontal, -8)
            content()
        }
    }
}
