@testable import AgenthailHelpers
import Foundation

struct TileOrderVectors: Decodable {
    struct Session: Decodable { let currentReason: String?; let waitingOn: Bool; let lastActive: String? }
    struct Lead: Decodable { let session: Session? }
    struct Project: Decodable {
        let id: String
        let sessionCount: Int
        var lastActive: String? = nil
        var archived: Bool? = nil
        var lead: Lead? = nil
        var waiting: Int? = nil
    }
    struct Case: Decodable { let name: String; let limit: Int; let projects: [Project]; let shown: [String]; let more: [String] }
    let now: String
    let cases: [Case]
}

@main
struct TileOrderVectorsTest {
    static let fixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/tile-order-vectors.json")

    static func main() throws {
        let vectors = try JSONDecoder().decode(TileOrderVectors.self, from: Data(contentsOf: fixture))
        let now = TimestampParser.date(vectors.now)!
        for vector in vectors.cases {
            let projects = vector.projects.map { project in
                ProjectState(id: project.id, displayName: project.id, coordinators: project.lead.map { _ in [ProjectState.Lead(name: "lead", surface: "claude")] },
                             archived: project.archived, activity: ProjectActivity(id: project.id, lastActive: project.lastActive, sessionCount: project.sessionCount, checkouts: []))
            }
            var waiting: [String: Int] = [:]
            var leads: [String: ProjectTiles.Lead] = [:]
            for project in vector.projects {
                waiting[project.id] = project.waiting
                if let session = project.lead?.session {
                    leads[project.id] = ProjectTiles.Lead(currentReason: session.currentReason, waiting: session.waitingOn, lastActive: session.lastActive)
                }
            }
            let tiles = ProjectTiles.order(projects, limit: vector.limit, waiting: waiting, leads: leads, now: now)
            check(tiles.shown.map(\.id) == vector.shown, "\(vector.name): shown \(tiles.shown.map(\.id))")
            check(tiles.more.map(\.id) == vector.more, "\(vector.name): more \(tiles.more.map(\.id))")
        }
        print("tile order vectors ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
