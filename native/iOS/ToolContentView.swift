import SwiftUI

struct ToolContentView: View {
    let item: TimelineItem
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            switch ToolPresentation(name: item.title, text: item.text).content {
            case .command(let command, let directory):
                if let directory { Label(directory, systemImage: "folder").font(.caption).textSelection(.enabled) }
                code(command)
                rawInput
            case .edit(let path, let before, let after):
                Label(path, systemImage: "doc.text").font(.callout).textSelection(.enabled)
                Label("Removed", systemImage: "minus.circle").font(.caption.weight(.semibold))
                code(before).padding(8).background(Color.red.opacity(0.08))
                Label("Added", systemImage: "plus.circle").font(.caption.weight(.semibold))
                code(after).padding(8).background(Color.green.opacity(0.08))
                rawInput
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
                code(item.text.isEmpty ? "No additional details were recorded." : item.text)
            }
        }.frame(maxWidth: .infinity, alignment: .leading)
    }
    private var rawInput: some View {
        DisclosureGroup("Full recorded input") { code(item.text).padding(.top, 8) }.font(.footnote)
    }
    private func code(_ text: String) -> some View {
        TranscriptCode(text: text)
    }
}
