import Foundation

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

    static func label(_ items: [TimelineItem]) -> String {
        let calls = items.filter(isCall)
        guard !calls.isEmpty else { return "Thought" }
        var counts = Array(repeating: 0, count: categories.count)
        var other = 0
        for call in calls {
            if let index = categories.firstIndex(where: { $0.names.contains(call.title) }) {
                counts[index] += 1
            } else {
                other += 1
            }
        }
        var phrases = counts.enumerated()
            .filter { $0.element > 0 }
            .sorted { $0.element != $1.element ? $0.element > $1.element : $0.offset < $1.offset }
            .map { (count: $0.element, text: $0.element == 1 ? categories[$0.offset].singular : String(format: categories[$0.offset].plural, $0.element)) }
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
