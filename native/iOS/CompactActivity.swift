import SwiftUI

struct TimelineGroup: Identifiable {
    let id: String
    var items: [TimelineItem]
    var isMessage: Bool { items.first?.kind == "message" }
    var callCount: Int { items.filter { $0.kind == "toolCall" }.count }
    var errorCount: Int { items.filter { $0.status == "error" }.count }
    var isActivity: Bool { items.allSatisfy { ["toolCall", "toolResult", "reasoning"].contains($0.kind) } }

    var summary: String {
        var counts: [String: Int] = [:]
        var order: [String] = []
        for item in items where item.kind == "toolCall" {
            let presentation = ToolPresentation(name: item.title, text: item.text)
            let category: String
            switch presentation.title {
            case "Run command": category = "command"
            case "Read file": category = "file read"
            case "Write file", "Edit file": category = "file change"
            case "Search files", "Search the web": category = "search"
            case "Update plan": category = "plan update"
            default: category = presentation.title
            }
            if counts[category] == nil { order.append(category) }
            counts[category, default: 0] += 1
        }
        let parts = order.map { category in
            let count = counts[category]!
            let suffix = count == 1 ? "" : category == "search" ? "es" : "s"
            return "\(count) \(category)\(suffix)"
        }
        let base = parts.isEmpty ? (items.contains { $0.kind == "reasoning" } ? "Reasoning" : "Tool output") : parts.joined(separator: ", ")
        return base + (errorCount > 0 ? " (\(errorCount) failed)" : "")
    }

    static func make(_ items: [TimelineItem]) -> [TimelineGroup] {
        var groups: [TimelineGroup] = []
        for item in items {
            let activity = ["toolCall", "toolResult", "reasoning"].contains(item.kind)
            if activity, groups.last?.isActivity == true { groups[groups.count - 1].items.append(item) }
            else { groups.append(TimelineGroup(id: item.id, items: [item])) }
        }
        return groups
    }

    var invocations: [TimelineGroup] {
        var groups: [TimelineGroup] = []
        var calls: [String: Int] = [:]
        for item in items {
            if item.kind == "message" { calls.removeAll() }
            if item.kind == "toolResult", let callID = item.callId, let index = calls[callID] {
                groups[index].items.append(item)
            } else {
                if item.kind == "toolCall", let callID = item.callId { calls[callID] = groups.count }
                groups.append(TimelineGroup(id: item.id, items: [item]))
            }
        }
        return groups
    }
}

struct CompactActivityGroup: View {
    let group: TimelineGroup
    var onInspect: () -> Void = {}
    @State private var expanded = false

    var body: some View {
        if group.isActivity {
            VStack(alignment: .leading, spacing: 10) {
                Button { onInspect(); expanded.toggle() } label: {
                    HStack(spacing: 8) {
                        Text(group.summary).font(.subheadline.weight(.medium)).lineLimit(2)
                        Image(systemName: expanded ? "chevron.down" : "chevron.right").font(.caption.weight(.semibold))
                    }
                    .foregroundStyle(group.errorCount > 0 ? .orange : .secondary)
                    .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
                }.buttonStyle(.plain)
                    .accessibilityIdentifier("activity-" + group.id)
                    .accessibilityHint("Expand recorded tools, results and reasoning")
                if expanded {
                    ForEach(group.invocations) { invocation in
                        if let call = invocation.items.first, call.kind == "toolCall" {
                            ToolActivityRow(call: call, results: Array(invocation.items.dropFirst()))
                        } else { ForEach(invocation.items) { IOSTimelineRow(item: $0) } }
                    }
                }
            }
        } else {
            ForEach(group.items) { IOSTimelineRow(item: $0) }
        }
    }
}

struct ToolActivityRow: View {
    let call: TimelineItem
    let results: [TimelineItem]
    var standaloneRecord = false
    @State private var expanded = false
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize

    private var presentation: ToolPresentation { ToolPresentation(name: call.title, text: call.text) }
    private var failed: Bool { results.contains { $0.status == "error" } }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { expanded.toggle() } label: {
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: presentation.symbol).frame(width: 20).padding(.top, 2)
                    VStack(alignment: .leading, spacing: 4) {
                        let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(alignment: .firstTextBaseline))
                        layout {
                            Text(presentation.title).font(.subheadline.weight(.medium)).foregroundStyle(.primary)
                            if !dynamicTypeSize.isAccessibilitySize { Spacer(minLength: 4) }
                            Text(failed ? "Failed" : standaloneRecord ? "Input" : results.isEmpty ? "No result yet" : "Output recorded")
                                .font(.caption).foregroundStyle(failed ? .red : .secondary)
                        }
                        Text(presentation.summary).font(.callout.monospaced()).foregroundStyle(.secondary).lineLimit(expanded ? nil : 2)
                        if failed, let result = results.first(where: { $0.status == "error" }) {
                            Text(result.text).font(.footnote).foregroundStyle(.red).lineLimit(3)
                        }
                    }
                    Image(systemName: expanded ? "chevron.down" : "chevron.right").font(.caption).padding(.top, 3)
                }
                .padding(.vertical, 12).padding(.horizontal, 12)
                .frame(minHeight: 44)
                .contentShape(Rectangle())
            }.buttonStyle(.plain)
            .accessibilityHint("Show recorded input and output")
            if expanded {
                VStack(alignment: .leading, spacing: 12) {
                    ToolContentView(item: call)
                    ForEach(results) { result in
                        Divider()
                        HStack {
                            Text(result.status == "error" ? "Error output" : "Output").font(.caption.weight(.semibold))
                            Spacer()
                            SessionTimestamp(value: result.timestamp)
                        }.foregroundStyle(.secondary)
                        ToolOutput(text: result.text)
                        if result.truncated { shortened }
                    }
                    if call.truncated { shortened }
                    if let id = call.callId {
                        Text("Call \(id)").font(.caption2.monospaced()).foregroundStyle(.tertiary).textSelection(.enabled)
                    }
                }.padding([.horizontal, .bottom], 12)
            }
        }
        .background(SessionStyle.surface, in: RoundedRectangle(cornerRadius: 12))
    }

    private var shortened: some View {
        Label("Output shortened by the host", systemImage: "text.badge.ellipsis").font(.caption).foregroundStyle(.secondary)
    }
}

struct ToolOutput: View {
    let text: String
    @State private var showAll = false

    private var preview: String { String(text.split(separator: "\n", omittingEmptySubsequences: false).prefix(24).joined(separator: "\n").prefix(3000)) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            TranscriptCode(text: text.isEmpty ? "The tool returned an empty result." : showAll ? text : preview)
            if preview.count < text.count {
                Button(showAll ? "Show less output" : "Show full output") { showAll.toggle() }
                    .font(.footnote).frame(minHeight: 44)
            }
            Button("Copy output", systemImage: "doc.on.doc") { UIPasteboard.general.string = text }
                .font(.footnote).frame(minHeight: 44)
        }
    }
}
