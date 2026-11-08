import SwiftUI
import XCTest
@testable import Agenthail

final class ProjectTilesTests: XCTestCase {
    @MainActor
    func testTilesCountDecisionsUnderTheProjectIdsTheScreensOpen() throws {
        var snapshot = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as? [String: Any])
        snapshot["projects"] = [
            ["id": "p-api", "displayName": "api", "checkouts": [], "coordinators": [["name": "lead", "surface": "claude", "sessionId": "demo"]]],
            ["id": "hpeer::p-tool", "displayName": "tool", "checkouts": [], "coordinators": [["name": "lead", "surface": "claude"]]],
        ]
        let model = AgenthailIOSTests.computerModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: JSONSerialization.data(withJSONObject: snapshot)))

        let projects = model.qualifiedProjects
        XCTAssertEqual(projects.map(\.id), ["computer::p-api", "hpeer::p-tool"])
        XCTAssertEqual(model.colourID(of: "computer::p-api"), "computer::p-api", "a keyless project's colour id names the computer that owns it")
        XCTAssertEqual(model.colourID(of: "hpeer::p-tool"), "hpeer::p-tool", "a relayed project keeps its owner")
        let lead = try XCTUnwrap(model.snapshot?.sessions.first { $0.id == "computer::demo" })
        XCTAssertEqual(model.tileLeads, ["computer::p-api": ProjectTiles.Lead(lead)], "a lead is found by its session on the computer that owns the project; a lead with no session yet has none")

        func item(_ session: String, project: String, host: String) -> DecisionItem {
            var item = DecisionItem(group: .waiting, sessionId: host + "::" + session, projectId: project, kind: "ask", reason: "r", answerable: false)
            item.hostID = host
            return item
        }
        let inbox = DecisionInbox(decisions: [
            item("a", project: "computer::p-api", host: "computer"),
            item("b", project: "computer::p-api", host: "computer"),
            item("c", project: "hpeer::p-tool", host: "hpeer"),
            item("d", project: "computer::gone", host: "computer"),
        ])
        XCTAssertEqual(inbox.needsYou(in: projects), ["computer::p-api": 2, "hpeer::p-tool": 1])
        XCTAssertEqual(inbox.decisions(in: "computer::p-api", of: projects).map(\.sessionId), ["computer::a", "computer::b"])
        XCTAssertTrue(model.projects["computer::p-api"] != nil, "a tile's id opens its project screen")
    }

    func testColourIdsMatchTheMacAndDashboardVectors() throws {
        struct Vectors: Decodable {
            struct Case: Decodable { let id: String; let colour: Int }
            struct Owned: Decodable { let name: String; let id: String; let owner: String; let colourId: String; let colour: Int }
            let count: Int
            let cases: [Case]
            let colourIds: [Owned]
        }
        let url = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "project-colour-vectors", withExtension: "json"))
        let vectors = try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: url))
        XCTAssertEqual(PaletteTokens.projects.count, vectors.count)
        for vector in vectors.cases { XCTAssertEqual(ProjectTiles.colour(vector.id), vector.colour, vector.id) }
        for vector in vectors.colourIds {
            let id = ProjectTiles.colourID(vector.id, owner: vector.owner)
            XCTAssertEqual(id, vector.colourId, vector.name)
            XCTAssertEqual(ProjectTiles.colour(id), vector.colour, vector.name)
        }
    }

    func testTileOrderMatchesTheMacAndDashboardVectors() throws {
        struct Vectors: Decodable {
            struct Session: Decodable { let currentReason: String?; let waitingOn: Bool; let lastActive: String? }
            struct Lead: Decodable { let session: Session? }
            struct Project: Decodable { let id: String; let sessionCount: Int; let lastActive: String?; let archived: Bool?; let lead: Lead?; let waiting: Int? }
            struct Case: Decodable { let name: String; let limit: Int; let projects: [Project]; let shown: [String]; let more: [String] }
            let now: String
            let cases: [Case]
        }
        let url = try XCTUnwrap(Bundle(for: Self.self).url(forResource: "tile-order-vectors", withExtension: "json"))
        let vectors = try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: url))
        let now = try XCTUnwrap(TimestampParser.date(vectors.now))
        for vector in vectors.cases {
            let projects = vector.projects.map { project in
                ProjectState(id: project.id, displayName: project.id, coordinators: project.lead.map { _ in [ProjectState.Lead(name: "lead", surface: "claude")] },
                             archived: project.archived, activity: ProjectActivity(id: project.id, lastActive: project.lastActive, sessionCount: project.sessionCount, checkouts: []))
            }
            let waiting = Dictionary(uniqueKeysWithValues: vector.projects.compactMap { project in project.waiting.map { (project.id, $0) } })
            let leads = Dictionary(uniqueKeysWithValues: vector.projects.compactMap { project in
                project.lead?.session.map { (project.id, ProjectTiles.Lead(currentReason: $0.currentReason, waiting: $0.waitingOn, lastActive: $0.lastActive)) }
            })
            let tiles = ProjectTiles.order(projects, limit: vector.limit, waiting: waiting, leads: leads, now: now)
            XCTAssertEqual(tiles.shown.map(\.id), vector.shown, vector.name)
            XCTAssertEqual(tiles.more.map(\.id), vector.more, vector.name)
        }
    }

    func testAPhoneShowsFourProjectTilesAndAnIPadSix() {
        XCTAssertEqual(PhoneProjectTiles.limit(.compact), 4)
        XCTAssertEqual(PhoneProjectTiles.limit(.regular), 6)
        XCTAssertEqual(PhoneProjectTiles.moreTitle(1), "1 more project")
        XCTAssertEqual(PhoneProjectTiles.moreTitle(3), "3 more projects")
    }
}
