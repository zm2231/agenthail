@testable import AgenthailHelpers
import Foundation

@main
struct ProjectTilesTest {
    static func main() {
        func project(_ id: String, sessions: Int, lastActive: String? = nil, lead: Bool = false, archived: Bool = false) -> ProjectState {
            ProjectState(id: id, displayName: id, coordinators: lead ? [ProjectState.Lead(name: "lead", surface: "claude")] : nil, archived: archived,
                         activity: ProjectActivity(id: id, lastActive: lastActive, sessionCount: sessions, checkouts: []))
        }
        let projects = [
            project("old", sessions: 2, lastActive: "2026-11-01T10:00:00Z"),
            project("newest", sessions: 1, lastActive: "2026-11-08T10:00:00Z"),
            project("lead-only", sessions: 0, lead: true),
            project("empty", sessions: 0),
            project("shelved", sessions: 4, lastActive: "2026-11-08T11:00:00Z", archived: true),
            project("middle", sessions: 3, lastActive: "2026-11-05T10:00:00Z"),
        ]
        let tiles = ProjectTiles.order(projects, limit: 3)
        check(tiles.shown.map(\.id) == ["newest", "middle", "old"], "tiles are the most recently active open projects: \(tiles.shown.map(\.id))")
        check(tiles.more.map(\.id) == ["empty", "lead-only", "shelved"], "the rest, archived ones included, go under more: \(tiles.more.map(\.id))")
        let roomy = ProjectTiles.order(projects, limit: 5)
        check(roomy.shown.map(\.id) == ["newest", "middle", "old", "lead-only"], "a project with no sessions gets a tile when it has a lead: \(roomy.shown.map(\.id))")
        check(roomy.more.map(\.id) == ["empty", "shelved"], "an archived project only appears under more")
        check(ProjectTiles.letter("agenthail") == "A" && ProjectTiles.letter("_tools") == "T" && ProjectTiles.letter("") == "·", "a tile's letter is the name's first letter or digit")
        let vectors = try! JSONDecoder().decode(ColourVectors.self, from: Data(contentsOf: colourFixture))
        check(PaletteTokens.projects.count == vectors.count, "\(PaletteTokens.projects.count) project colours, the vectors expect \(vectors.count)")
        for vector in vectors.cases {
            check(ProjectTiles.colour(vector.id) == vector.colour, "\(vector.id) takes colour \(ProjectTiles.colour(vector.id)), the dashboard gives it \(vector.colour)")
        }
        print("project tiles ok")
    }

    struct ColourVectors: Decodable {
        struct Case: Decodable { let id: String; let colour: Int }
        let count: Int
        let cases: [Case]
    }

    static let colourFixture = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appendingPathComponent("scripts/fixtures/project-colour-vectors.json")

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
