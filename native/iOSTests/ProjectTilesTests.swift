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
        XCTAssertEqual(model.projects["computer::p-api"]?.state.id, "p-api", "the colour hashes the id the computer itself uses, as the Mac and dashboard do")

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

    func testAPhoneShowsFourProjectTilesAndAnIPadSix() {
        XCTAssertEqual(PhoneProjectTiles.limit(.compact), 4)
        XCTAssertEqual(PhoneProjectTiles.limit(.regular), 6)
        XCTAssertEqual(PhoneProjectTiles.moreTitle(1), "1 more project")
        XCTAssertEqual(PhoneProjectTiles.moreTitle(3), "3 more projects")
    }
}
