import SwiftUI

struct ClaudeObservationsSection: View {
    let detail: SessionDetail?

    var body: some View {
        if let runs = detail?.claudeRuns {
            group("Claude runs") {
                if runs.isEmpty {
                    Text("No observed Claude runs.")
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                }
                ForEach(runs) { run in
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Job: \(run.jobId)")
                            .textSelection(.enabled)
                        let facts = ClaudeRunFacts.lines(run)
                        if !facts.isEmpty {
                            Text(facts.joined(separator: " · "))
                                .font(.system(size: DesktopType.meta))
                                .foregroundStyle(DesktopPalette.text2)
                                .textSelection(.enabled)
                        }
                    }
                }
            }
        }
        if let errors = detail?.metadataErrors, !errors.isEmpty {
            group("Not available right now") {
                ForEach(MetadataWarnings.lines(errors), id: \.self) { line in
                    Text(line)
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                        .textSelection(.enabled)
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
