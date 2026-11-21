import Foundation

enum TranscriptFocus {
    static let defaultsKey = "transcriptFocus"

    static let workKinds: Set<String> = ["toolCall", "toolResult", "reasoning", "tool", "command"]

    /// Each turn's tool work gathers into one run just before the turn's last reply, so the turn reads as what was done followed by what was said.
    /// Asks and their answers stay where they were asked; notices and events other than the turn's duration are left out.
    static func focused(_ items: [TimelineItem]) -> [TimelineItem] {
        var out: [TimelineItem] = []
        var work: [TimelineItem] = []
        var reply: Int?
        var kept = Set<String>()
        func endTurn() {
            if let reply { out.insert(contentsOf: work, at: reply) } else { out += work }
            work = []
            reply = nil
        }
        for item in items {
            if item.kind == "notice" || item.kind == "event" && item.title != "Turn duration" { continue }
            if startsTurn(item) {
                endTurn()
                out.append(item)
            } else if isAsk(item) || item.agent != nil {
                if let call = item.callId { kept.insert(call) }
                out.append(item)
            } else if item.agent == nil, workKinds.contains(item.kind), !(item.kind == "toolResult" && item.callId.map(kept.contains) == true) {
                work.append(item)
            } else {
                if item.role == "assistant", ["message", "text"].contains(item.kind) { reply = out.count }
                out.append(item)
            }
        }
        endTurn()
        return out
    }

    /// Tracks which tool items belong to an ask, so Focus keeps an ask out of the work run beside it.
    struct AskRuns {
        private var calls = Set<String>()
        private var lastWasAsk: Bool?

        mutating func breaks(before item: TimelineItem) -> Bool {
            let ask = TranscriptFocus.isAsk(item) || item.kind == "toolResult" && item.callId.map(calls.contains) == true
            if TranscriptFocus.isAsk(item), let call = item.callId { calls.insert(call) }
            defer { lastWasAsk = ask }
            return lastWasAsk.map { $0 != ask } ?? false
        }
    }

    static func startsTurn(_ item: TimelineItem) -> Bool {
        if item.agent?.isIncoming == true { return true }
        return item.role == "user" && ["message", "text", "file", "attachment"].contains(item.kind) && !item.isInstructions
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
        item.kind == "toolCall" && (["AskUserQuestion", "request_user_input", "agenthail_ask"].contains(item.title) || item.title.hasSuffix("__agenthail_ask"))
    }
}
