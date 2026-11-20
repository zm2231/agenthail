import SwiftUI

extension EnvironmentValues {
    @Entry var expandAgentMessages = false
}

struct AgentMessage: Decodable, Equatable, Hashable {
    let direction: String
    let kind: String
    var peer: String? = nil
    var channel: String? = nil
    let summary: String
    var body: String? = nil

    var isIncoming: Bool { direction == "in" }
    var isSend: Bool { direction == "out" && kind == "send" }

    var chip: String {
        let name = peer.flatMap { $0.isEmpty ? nil : $0 } ?? "another agent"
        guard kind == "peer", let channel, !channel.isEmpty else { return name }
        return "\(name) · #\(channel)"
    }
}

enum AgentFold {
    static let defaultsKey = "expandAgentMessages"

    static func runs(_ agents: [AgentMessage?]) -> [Range<Int>] {
        var runs: [Range<Int>] = []
        var start: Int?
        for index in agents.indices {
            if agents[index]?.isIncoming == true {
                if start == nil { start = index }
                continue
            }
            if let first = start, index - first >= 2 { runs.append(first..<index) }
            start = nil
        }
        return runs
    }

    static func label(_ agents: [AgentMessage]) -> String {
        let count = agents.count
        if agents.allSatisfy({ $0.kind == "peer" }) {
            let senders = Set(agents.map(\.chip))
            if senders.count == 1, let sender = senders.first { return "\(count) messages from \(sender)" }
            return "\(count) messages from agents"
        }
        var order: [String] = []
        var counts: [String: Int] = [:]
        for agent in agents {
            if counts[agent.kind] == nil { order.append(agent.kind) }
            counts[agent.kind, default: 0] += 1
        }
        let parts = order.map { kind -> String in
            let n = counts[kind] ?? 0
            let noun = nouns[kind] ?? ("update", "updates")
            return "\(n) \(n == 1 ? noun.0 : noun.1)"
        }
        return "\(count) updates · " + parts.joined(separator: ", ")
    }

    static func senders(_ agents: [AgentMessage]) -> String {
        var seen: [String] = []
        for chip in agents.map(\.chip) where !seen.contains(chip) { seen.append(chip) }
        return seen.joined(separator: ", ")
    }

    private static let nouns: [String: (String, String)] = [
        "peer": ("agent message", "agent messages"),
        "task": ("task notice", "task notices"),
        "continuation": ("automatic continuation", "automatic continuations"),
        "notice": ("Agenthail notice", "Agenthail notices"),
    ]
}

extension AgentMessage {
    static func inline(_ text: String) -> AttributedString {
        (try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(text)
    }
}

extension AgentMessage {
    var repeatKey: String? {
        guard isIncoming, kind == "notice" else { return nil }
        return (peer ?? "") + "\n" + summary
    }

    static func chip(_ chip: String, repeats: Int) -> String {
        repeats > 1 ? "\(chip) ×\(repeats)" : chip
    }
}

enum AgentRowStyle {
    static let previewLines = 1

    static func expandable(text: String, truncated: Bool, files: Int) -> Bool {
        truncated || files > 0 || text.trimmingCharacters(in: .whitespacesAndNewlines).contains("\n") || TranscriptLinks.hasAddress(text)
    }
}

enum AgentRepeat {
    static func runs(_ keys: [String?]) -> [Range<Int>] {
        var runs: [Range<Int>] = []
        var start = 0
        for index in keys.indices.dropFirst() + [keys.count] {
            let continues = index < keys.count && keys[index] != nil && keys[index] == keys[start]
            if continues { continue }
            if keys.indices.contains(start), keys[start] != nil, index - start >= 2 { runs.append(start..<index) }
            start = index
        }
        return runs
    }
}
