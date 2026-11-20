import Foundation

struct AgentEntry: Equatable {
    let item: TimelineItem
    let agent: AgentMessage
    var result: TimelineItem? = nil
    var repeats = 1

    var failed: Bool { result.map(ToolRunSummary.isFailure) ?? false }

    var chip: String {
        guard agent.kind == "task", let status = item.status, !status.isEmpty else { return AgentMessage.chip(agent.chip, repeats: repeats) }
        return AgentMessage.chip("\(agent.chip) · \(status)", repeats: repeats)
    }
}

struct AgentFoldEntry: Equatable {
    let label: String
    let senders: String
    let members: [TranscriptBlock]
}

extension TranscriptBlock {
    var incomingAgent: AgentMessage? {
        guard case .agent(let entry) = kind, entry.agent.isIncoming else { return nil }
        return entry.agent
    }

    static func repeated(_ blocks: [TranscriptBlock]) -> [TranscriptBlock] {
        var merged: [TranscriptBlock] = []
        var next = 0
        for run in AgentRepeat.runs(blocks.map { $0.incomingAgent?.repeatKey }) {
            merged += blocks[next..<run.lowerBound]
            let last = blocks[run.upperBound - 1]
            if case .agent(var entry) = last.kind {
                entry.repeats = run.count
                merged.append(TranscriptBlock(id: blocks[run.lowerBound].id, kind: .agent(entry), truncated: last.truncated, timestamp: last.timestamp))
            }
            next = run.upperBound
        }
        merged += blocks[next...]
        return merged
    }

    static func folded(_ blocks: [TranscriptBlock]) -> [TranscriptBlock] {
        let agents = blocks.map(\.incomingAgent)
        var folded: [TranscriptBlock] = []
        var next = 0
        for run in AgentFold.runs(agents) {
            folded += blocks[next..<run.lowerBound]
            let members = Array(blocks[run])
            let messages = members.flatMap { member -> [AgentMessage] in
                guard case .agent(let entry) = member.kind, entry.agent.isIncoming else { return [] }
                return Array(repeating: entry.agent, count: entry.repeats)
            }
            let entry = AgentFoldEntry(label: AgentFold.label(messages), senders: AgentFold.senders(messages), members: members)
            folded.append(TranscriptBlock(id: TranscriptRowID.fold(members[0].id), kind: .agentFold(entry), timestamp: members.last?.timestamp))
            next = run.upperBound
        }
        folded += blocks[next...]
        return folded
    }
}
