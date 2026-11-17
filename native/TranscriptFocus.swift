import Foundation

enum TranscriptFocus {
    static let defaultsKey = "transcriptFocus"

    static func kept(_ items: [TimelineItem]) -> [TimelineItem] {
        var calls = Set<String>()
        return items.filter { item in
            if item.agent != nil || isAsk(item) {
                if let call = item.callId { calls.insert(call) }
                return true
            }
            switch item.kind {
            case "message", "text", "file", "attachment": return true
            case "event": return item.title == "Turn duration"
            case "toolResult": return item.callId.map(calls.contains) ?? false
            default: return false
            }
        }
    }

    static func shown(_ items: [TimelineItem]) -> [TimelineItem] {
        var hidden = Set<String>()
        for item in items where item.kind == "toolCall" && isEndTurn(item.title) {
            if let call = item.callId { hidden.insert(call) }
        }
        return items.filter { item in
            if item.kind == "toolCall" && isEndTurn(item.title) { return false }
            if item.kind == "toolResult", let call = item.callId { return !hidden.contains(call) }
            return true
        }
    }

    static func isEndTurn(_ tool: String) -> Bool {
        tool == "agenthail_end_turn" || tool.hasPrefix("mcp__") && tool.hasSuffix("__agenthail_end_turn")
    }

    static func isAsk(_ item: TimelineItem) -> Bool {
        item.kind == "toolCall" && (["AskUserQuestion", "request_user_input"].contains(item.title) || item.title.hasSuffix("__agenthail_ask"))
    }
}
