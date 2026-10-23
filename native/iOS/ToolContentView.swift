import SwiftUI

struct ToolContentView: View {
    let item: TimelineItem
    var body: some View {
        let presentation = ToolPresentation(name: item.title, text: item.text)
        let diffs = presentation.diffs
        VStack(alignment: .leading, spacing: 12) {
            switch presentation.content {
            case .command(let command, let directory):
                if let directory { Label(directory, systemImage: "folder").font(.caption).textSelection(.enabled) }
                code(command)
                rawInput
            case .edit:
                changes(diffs)
            case .plan(let steps):
                ForEach(Array(steps.enumerated()), id: \.offset) { _, step in
                    Label {
                        VStack(alignment: .leading) {
                            Text(step.0)
                            Text(step.1.replacingOccurrences(of: "_", with: " ")).font(.caption).foregroundStyle(.secondary)
                        }
                    } icon: { Image(systemName: step.1 == "completed" ? "checkmark.circle" : step.1 == "in_progress" ? "circle.lefthalf.filled" : "circle") }
                }
                rawInput
            case .raw:
                if diffs.isEmpty {
                    code(item.text.isEmpty ? "No additional details were recorded." : presentation.readableInput)
                } else {
                    changes(diffs)
                }
            }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
    @ViewBuilder
    private func changes(_ diffs: [FileDiff]) -> some View {
        ForEach(diffs, id: \.path) { file in
            Label(file.path, systemImage: "doc.text").font(.callout).textSelection(.enabled)
            DiffView(id: item.id + ":" + file.path, hunks: file.hunks)
        }
        rawInput
    }

    private var rawInput: some View {
        DisclosureGroup("Full recorded input") { code(item.text).padding(.top, 8) }.font(.footnote)
    }
    private func code(_ text: String) -> some View {
        TranscriptCode(text: text)
    }
}
