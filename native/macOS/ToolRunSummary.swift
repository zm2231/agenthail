import Foundation

struct ToolInvocation: Identifiable {
    let call: TimelineItem?
    var results: [TimelineItem]

    var id: String { call?.id ?? results.first?.id ?? "" }
}

enum ToolRunSummary {
    private struct Category {
        let names: Set<String>
        let singular: String
        let plural: String
    }

    private static let categories: [Category] = [
        Category(names: ["Bash", "BashOutput", "exec", "exec_command", "shell", "local_shell"], singular: "ran 1 command", plural: "ran %d commands"),
        Category(names: ["Read", "read_file", "view_image", "NotebookRead"], singular: "read 1 file", plural: "read %d files"),
        Category(names: ["Edit", "MultiEdit", "Write", "NotebookEdit", "apply_patch"], singular: "edited 1 file", plural: "edited %d files"),
        Category(names: ["Grep", "Glob", "LS", "ToolSearch"], singular: "searched once", plural: "searched %d times"),
        Category(names: ["WebFetch", "WebSearch", "web_search", "Web search"], singular: "made 1 web lookup", plural: "made %d web lookups"),
        Category(names: ["Agent", "Task", "spawn_agent"], singular: "started 1 subagent", plural: "started %d subagents"),
    ]

    static func isCall(_ item: TimelineItem) -> Bool {
        item.kind == "toolCall" || item.kind == "tool" || item.kind == "command"
    }

    static func isFailure(_ item: TimelineItem) -> Bool {
        item.status == "failed" || item.status == "error"
    }

    static func invocations(_ items: [TimelineItem]) -> [ToolInvocation] {
        var invocations: [ToolInvocation] = []
        var callIndex: [String: Int] = [:]
        for item in items {
            if isCall(item) {
                if let callID = item.callId { callIndex[callID] = invocations.count }
                invocations.append(ToolInvocation(call: item, results: []))
            } else if item.kind == "toolResult" {
                if let callID = item.callId, let index = callIndex[callID] {
                    invocations[index].results.append(item)
                } else {
                    invocations.append(ToolInvocation(call: nil, results: [item]))
                }
            }
        }
        return invocations
    }

    static func outputText(_ text: String) -> String {
        guard text.hasPrefix("["), let blocks = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [[String: Any]], !blocks.isEmpty else { return text }
        let texts = blocks.compactMap { $0["text"] as? String }
        return texts.count == blocks.count ? texts.joined() : text
    }

    static func outputPreview(_ text: String) -> String {
        String(text.split(separator: "\n", omittingEmptySubsequences: false).prefix(24).joined(separator: "\n").prefix(3000))
    }

    static func mcpParts(_ title: String) -> (server: String, tool: String)? {
        guard title.hasPrefix("mcp__") else { return nil }
        let rest = title.dropFirst(5)
        guard let separator = rest.range(of: "__") else { return nil }
        return (String(rest[..<separator.lowerBound]), String(rest[separator.upperBound...]))
    }

    static func label(_ items: [TimelineItem]) -> String {
        let calls = items.filter(isCall)
        guard !calls.isEmpty else { return "Thought" }
        var counts = Array(repeating: 0, count: categories.count)
        var servers: [String: Int] = [:]
        var serverOrder: [String] = []
        var other = 0
        for call in calls {
            if let index = categories.firstIndex(where: { $0.names.contains(call.title) }) {
                counts[index] += 1
            } else if let parts = mcpParts(call.title) {
                if servers[parts.server] == nil { serverOrder.append(parts.server) }
                servers[parts.server, default: 0] += 1
            } else {
                other += 1
            }
        }
        var phrases = counts.enumerated()
            .filter { $0.element > 0 }
            .map { (count: $0.element, text: $0.element == 1 ? categories[$0.offset].singular : String(format: categories[$0.offset].plural, $0.element)) }
        phrases += serverOrder.map { server in
            let count = servers[server] ?? 0
            return (count: count, text: count == 1 ? "used 1 \(server) tool" : "used \(count) \(server) tools")
        }
        phrases = phrases.enumerated()
            .sorted { $0.element.count != $1.element.count ? $0.element.count > $1.element.count : $0.offset < $1.offset }
            .map(\.element)
        if other > 0 {
            phrases.append((count: other, text: other == 1 ? "used 1 other tool" : "used \(other) other tools"))
        }
        let shown = phrases.prefix(2)
        let remaining = phrases.dropFirst(2).reduce(0) { $0 + $1.count }
        var label = shown.map(\.text).joined(separator: " and ")
        if remaining > 0 { label += " +\(remaining) other" }
        return label.prefix(1).uppercased() + label.dropFirst()
    }
}
