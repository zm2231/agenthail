import SwiftUI

struct TimelineGroup: Identifiable {
    let id: String
    var items: [TimelineItem]
    var isMessage: Bool { items.first?.kind == "message" }
    var callCount: Int { items.filter { $0.kind == "toolCall" }.count }
    var errorCount: Int { items.filter { $0.status == "error" }.count }
    var summary: String {
        let calls = items.filter { $0.kind == "toolCall" }
        if let latest = calls.last { return ToolPresentation(name: latest.title, text: latest.text).summary }
        return items.last?.title ?? "Activity"
    }
    static func make(_ items: [TimelineItem]) -> [TimelineGroup] {
        var groups: [TimelineGroup] = []
        for item in items {
            if item.kind != "message", let last = groups.last, !last.isMessage {
                groups[groups.count - 1].items.append(item)
            } else { groups.append(TimelineGroup(id: item.id, items: [item])) }
        }
        return groups
    }
}

struct CompactActivityGroup: View {
    let group: TimelineGroup
    @State private var expanded = false
    var body: some View {
        if group.isMessage {
            ForEach(group.items) { IOSTimelineRow(item: $0) }
        } else {
            DisclosureGroup(isExpanded: $expanded) {
                LazyVStack(alignment: .leading, spacing: 8) {
                    ForEach(group.items) { IOSTimelineRow(item: $0) }
                }.padding(.top, 8)
            } label: {
                VStack(alignment: .leading, spacing: 3) {
                    HStack {
                        Label(group.callCount > 0 ? "\(group.callCount) tool \(group.callCount == 1 ? "call" : "calls")" : "Agent activity", systemImage: "terminal")
                        Spacer()
                        if group.errorCount > 0 { Label("\(group.errorCount) failed", systemImage: "exclamationmark.circle").foregroundStyle(.red) }
                        Text("\(group.items.count) records").foregroundStyle(.secondary)
                    }.font(.caption.weight(.medium))
                    Text(group.summary).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    if group.items.contains(where: \.truncated) { Text("Includes shortened output").font(.caption2).foregroundStyle(.secondary) }
                }.frame(minHeight: 44)
            }
            .tint(.secondary)
            .padding(.horizontal, 10)
            .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 10))
            .accessibilityHint("Expand to inspect every recorded call, result and event")
        }
    }
}
