import SwiftUI

struct WorkingIndicator: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Image(systemName: "circle.dotted")
            .font(.system(size: 17)).foregroundStyle(SessionStyle.accent)
            .symbolEffect(.rotate, options: .repeating, isActive: !reduceMotion)
            .accessibilityLabel("Agent working")
    }
}

struct LiveSessionActivity: View {
    let items: [TimelineItem]

    private var latest: TimelineItem? {
        items.last { $0.kind == "toolCall" || $0.kind == "reasoning" || ($0.kind == "message" && $0.role == "assistant") }
    }
    private var summary: String? {
        guard let latest, latest.kind != "message" else { return nil }
        if latest.kind == "toolCall" {
            let tool = ToolPresentation(name: latest.title, text: latest.text)
            return tool.title + " · " + tool.summary
        }
        return latest.text.isEmpty ? nil : latest.text
    }

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            WorkingIndicator().padding(.top, 2)
            VStack(alignment: .leading, spacing: 5) {
                Text("Working on your Mac").font(.subheadline).foregroundStyle(.secondary)
                if let summary {
                    Text("Latest activity: " + summary).font(.footnote).foregroundStyle(.secondary).lineLimit(2)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.vertical, 12)
        .accessibilityElement(children: .combine)
    }
}
