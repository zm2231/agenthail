@testable import AgenthailHelpers
import Foundation

struct InboxStopVectors: Decodable {
    struct HostTag: Decodable { let hostId: String? }
    struct Case: Decodable {
        let name: String
        let localHosts: [String]
        let projects: [ProjectState]
        let sessions: [String: String]
        let attention: [AttentionState]
        let deliveries: [DeliveryProblem]
        let replacements: [LeadReplacement]
        let decisions: [DecisionItem]
        let decisionHosts: [HostTag]
        let stopped: [String]
        let decisionsShown: [String]
        let counts: [String: Int]
        let total: Int

        enum CodingKeys: String, CodingKey {
            case name, localHosts, projects, sessions, attention, deliveries, replacements, decisions, stopped, decisionsShown, counts, total
        }

        init(from decoder: Decoder) throws {
            let values = try decoder.container(keyedBy: CodingKeys.self)
            name = try values.decode(String.self, forKey: .name)
            localHosts = try values.decode([String].self, forKey: .localHosts)
            projects = try values.decode([ProjectState].self, forKey: .projects)
            sessions = try values.decode([String: String].self, forKey: .sessions)
            attention = try values.decode([AttentionState].self, forKey: .attention)
            deliveries = try values.decode([DeliveryProblem].self, forKey: .deliveries)
            replacements = try values.decode([LeadReplacement].self, forKey: .replacements)
            decisions = try values.decode([DecisionItem].self, forKey: .decisions)
            decisionHosts = try values.decode([HostTag].self, forKey: .decisions)
            stopped = try values.decode([String].self, forKey: .stopped)
            decisionsShown = try values.decode([String].self, forKey: .decisionsShown)
            counts = try values.decode([String: Int].self, forKey: .counts)
            total = try values.decode(Int.self, forKey: .total)
        }
    }
    let cases: [Case]
}

@main
struct InboxStopsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/inbox-stop-vectors.json")

    static func main() throws {
        let vectors = try JSONDecoder().decode(InboxStopVectors.self, from: Data(contentsOf: fixture))
        for vector in vectors.cases {
            let decisions = zip(vector.decisions, vector.decisionHosts).map { item, tag in
                var item = item
                item.hostID = tag.hostId
                return item
            }
            let stops = InboxStops.build(attention: vector.attention, deliveries: vector.deliveries, replacements: vector.replacements,
                                         inbox: DecisionInbox(decisions: decisions), localHosts: Set(vector.localHosts))
            check(stops.stopped.map(\.id) == vector.stopped, "\(vector.name): stopped \(stops.stopped.map(\.id))")
            check(stops.decisions.map(\.sessionId) == vector.decisionsShown, "\(vector.name): decisions \(stops.decisions.map(\.sessionId))")
            let counts = stops.needsYou(in: vector.projects) { vector.sessions[$0] }
            check(counts == vector.counts, "\(vector.name): counts \(counts)")
            for (project, count) in vector.counts {
                check(stops.rows(in: project, of: vector.projects) { vector.sessions[$0] }.count == count, "\(vector.name): \(project)'s screen lists what its tile counts")
            }
            check(stops.count == vector.total, "\(vector.name): total \(stops.count)")
        }
        print("inbox stops ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
