import XCTest
@testable import Agenthail

final class TranscriptReadingTests: XCTestCase {
    func testInjectedContextRemainsInspectableWithoutTreatingTheRequestAsSetup() throws {
        func record(_ text: String, role: String = "user") throws -> TimelineItem {
            let object: [String: Any] = ["id": "context", "kind": "message", "role": role, "title": role, "text": text, "truncated": false]
            return try JSONDecoder().decode(TimelineItem.self, from: JSONSerialization.data(withJSONObject: object))
        }
        let instructions = try record("# AGENTS.md instructions for /project\nKeep the original content.")
        XCTAssertEqual(TranscriptContext.title(for: instructions), "Repository instructions")
        XCTAssertEqual(TimelineGroup.make([instructions]).flatMap(\.items), [instructions])
        XCTAssertNil(TranscriptContext.title(for: try record("Please update AGENTS.md to document the release.")))
        XCTAssertNil(TranscriptContext.title(for: try record("# AGENTS.md instructions for /project\n\n## My request:\nFix the app.")))
        XCTAssertEqual(TranscriptContext.title(for: try record("<environment_context>workspace</environment_context>")), "Session environment")
        XCTAssertNil(TranscriptContext.title(for: try record("<environment_context>workspace</environment_context>\nRun the tests.")))
        XCTAssertEqual(TranscriptContext.title(for: try record("Runtime constraints", role: "developer")), "Agent instructions")
    }

    func testUntitledSessionsUseTheAgentAndWorkspaceOnBothScreens() {
        var session = SessionState(id: "session-id", surface: "codex", name: "", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil)
        session.cwd = "/projects/fieldnotes"
        XCTAssertEqual(SessionStyle.title(session), "Codex · fieldnotes")
    }
}
