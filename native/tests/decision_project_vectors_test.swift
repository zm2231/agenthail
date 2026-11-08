@testable import AgenthailHelpers
import Foundation

struct DecisionProjectVectors: Decodable {
    struct Case: Decodable {
        let name: String
        let session: String
        let host: String
        let projectId: String?
        let tile: String?
    }

    let projects: [ProjectState]
    let cases: [Case]
    let counts: [String: Int]
    let inboxTotal: Int
}

@main
struct DecisionProjectVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/decision-project-vectors.json")

    static func main() throws {
        let vectors = try JSONDecoder().decode(DecisionProjectVectors.self, from: Data(contentsOf: fixture))
        var decisions: [DecisionItem] = []
        for vector in vectors.cases {
            let item = DecisionInbox.qualified(DecisionItem(group: .waiting, sessionId: vector.session, projectId: vector.projectId, kind: "ask", reason: "r", answerable: false), host: vector.host) { id in
                vector.host.isEmpty || id.contains("::") ? id : vector.host + "::" + id
            }
            let tile = DecisionInbox.projectID(of: item, in: vectors.projects)
            check(tile == vector.tile, "\(vector.name): got \(tile ?? "no tile")")
            decisions.append(item)
        }
        let inbox = DecisionInbox(decisions: decisions)
        check(inbox.needsYou(in: vectors.projects) == vectors.counts, "tile badges: \(inbox.needsYou(in: vectors.projects))")
        check(inbox.decisions.count == vectors.inboxTotal, "the Inbox still counts decisions no tile holds")
        print("decision project vectors ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
