import Foundation

struct AgentEntry: Equatable {
    let item: TimelineItem
    let agent: AgentMessage
    var result: TimelineItem? = nil

    var failed: Bool { result.map(ToolRunSummary.isFailure) ?? false }

    var chip: String {
        guard agent.kind == "task", let status = item.status, !status.isEmpty else { return agent.chip }
        return "\(agent.chip) · \(status)"
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

    static func folded(_ blocks: [TranscriptBlock]) -> [TranscriptBlock] {
        let agents = blocks.map(\.incomingAgent)
        var folded: [TranscriptBlock] = []
        var next = 0
        for run in AgentFold.runs(agents) {
            folded += blocks[next..<run.lowerBound]
            let members = Array(blocks[run])
            let messages = agents[run].compactMap { $0 }
            let entry = AgentFoldEntry(label: AgentFold.label(messages), senders: AgentFold.senders(messages), members: members)
            folded.append(TranscriptBlock(id: "agents:" + members[0].id, kind: .agentFold(entry), timestamp: members.last?.timestamp))
            next = run.upperBound
        }
        folded += blocks[next...]
        return folded
    }
}
