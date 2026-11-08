import SwiftUI
import XCTest
@testable import Agenthail

final class ProjectTilesTests: XCTestCase {
    @MainActor
    func testTilesCountDecisionsUnderTheProjectIdsTheScreensOpen() throws {
        var snapshot = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(SessionPreview.snapshotJSON.utf8)) as? [String: Any])
        snapshot["projects"] = [
            ["id": "p-api", "displayName": "api", "coordinators": [["name": "lead", "surface": "claude"]]],
            ["id": "hpeer::p-tool", "displayName": "tool", "coordinators": [["name": "lead", "surface": "claude"]]],
        ]
        let model = AgenthailIOSTests.computerModel()
        model.setHostSnapshot(try JSONDecoder().decode(DashboardSnapshot.self, from: JSONSerialization.data(withJSONObject: snapshot)))

        let projects = model.qualifiedProjects
        XCTAssertEqual(projects.map(\.id), ["computer::p-api", "hpeer::p-tool"])
        XCTAssertEqual(model.colourID(of: "computer::p-api"), "computer::p-api", "a keyless project's colour id names the computer that owns it")
        XCTAssertEqual(model.colourID(of: "hpeer::p-tool"), "hpeer::p-tool", "a relayed project keeps its owner")

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

    func testAPhoneShowsFourProjectTilesAndAnIPadSix() {
        XCTAssertEqual(PhoneProjectTiles.limit(.compact), 4)
        XCTAssertEqual(PhoneProjectTiles.limit(.regular), 6)
        XCTAssertEqual(PhoneProjectTiles.moreTitle(1), "1 more project")
        XCTAssertEqual(PhoneProjectTiles.moreTitle(3), "3 more projects")
    }
}
