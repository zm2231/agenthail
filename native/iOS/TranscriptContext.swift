import SwiftUI

enum TranscriptContext {
    static func title(for item: TimelineItem) -> String? {
        guard item.kind == "message" else { return nil }
        if item.role == "system" { return "System instructions" }
        if item.role == "developer" { return "Agent instructions" }
        guard item.role == "user" else { return nil }
        let text = item.text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.contains("## My request:") else { return nil }
        if text.hasPrefix("# AGENTS.md instructions for ") { return "Repository instructions" }
        if text.hasPrefix("<environment_context>"), text.hasSuffix("</environment_context>") {
            return "Session environment"
        }
        if text.hasPrefix("<permissions instructions>"), text.hasSuffix("</permissions instructions>") {
            return "Session permissions"
        }
        return nil
    }
}

struct ContextRecordRow: View {
    let title: String
    let item: TimelineItem
    @State private var showingContent = false

    var body: some View {
        Button { showingContent = true } label: {
            HStack(spacing: 10) {
                Image(systemName: "doc.text")
                Text(title)
                Spacer(minLength: 8)
                Image(systemName: "chevron.right").font(.caption.weight(.semibold))
            }
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("context-" + item.id)
        .accessibilityHint("Read the full recorded instructions")
        .sheet(isPresented: $showingContent) {
            TranscriptDocument(title: title, text: item.text, markdown: true, truncated: item.truncated)
        }
    }
}

struct TranscriptDocument: View {
    let title: String
    let text: String
    let markdown: Bool
    var truncated = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if truncated {
                        Label("Content shortened by the host", systemImage: "text.badge.ellipsis")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                    if markdown { SessionMarkdown(text: text) }
                    else { Text(text).font(.body).lineSpacing(4).textSelection(.enabled) }
                }
                .frame(maxWidth: SessionStyle.readingWidth, alignment: .leading)
                .frame(maxWidth: .infinity, alignment: .center)
                .padding(20)
            }
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
                ToolbarItem(placement: .bottomBar) {
                    Button("Copy text", systemImage: "doc.on.doc") { UIPasteboard.general.string = text }
                }
            }
        }
    }
}
