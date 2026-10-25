@testable import AgenthailHelpers
import Foundation

@main
struct AgentFoldVectorsTest {
    struct Case: Decodable {
        let name: String
        let agents: [AgentMessage?]
        let runs: [[Int]]
        let labels: [String]
        let senders: [String]
    }

    struct Vectors: Decodable { let cases: [Case] }

    static func main() throws {
        let fixture = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("scripts/fixtures/agent-fold-vectors.json")
        let vectors = try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: fixture))
        for vector in vectors.cases {
            let runs = AgentFold.runs(vector.agents)
            expect(runs.map { [$0.lowerBound, $0.upperBound] } == vector.runs, "\(vector.name): runs \(runs)")
            let groups = runs.map { vector.agents[$0].compactMap { $0 } }
            expect(groups.map(AgentFold.label) == vector.labels, "\(vector.name): labels \(groups.map(AgentFold.label))")
            expect(groups.map(AgentFold.senders) == vector.senders, "\(vector.name): senders \(groups.map(AgentFold.senders))")
        }
        print("agent fold vectors passed: \(vectors.cases.count)")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
