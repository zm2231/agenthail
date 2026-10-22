import XCTest
@testable import Agenthail

final class TranscriptReadingTests: XCTestCase {
    func testInjectedContextRemainsInspectableWithoutTreatingTheRequestAsSetup() throws {
        func record(_ text: String, kind: String = "message", role: String? = "user") throws -> TimelineItem {
            var object: [String: Any] = ["id": "context", "kind": kind, "title": role ?? kind, "text": text, "truncated": false]
            if let role { object["role"] = role }
            return try JSONDecoder().decode(TimelineItem.self, from: JSONSerialization.data(withJSONObject: object))
        }
        let instructions = try record("# AGENTS.md instructions for /project\n<INSTRUCTIONS>Keep the original content.</INSTRUCTIONS>", kind: "instructions", role: nil)
        XCTAssertEqual(TranscriptContext.title(for: instructions), "Instructions")
        XCTAssertEqual(TimelineGroup.make([instructions]).flatMap(\.items), [instructions])
        XCTAssertEqual(TranscriptContext.title(for: try record("Runtime constraints", role: "developer")), "Instructions")
        XCTAssertEqual(TranscriptContext.title(for: try record("Base rules", role: "system")), "Instructions")
        XCTAssertNil(TranscriptContext.title(for: try record("Please update AGENTS.md to document the release.")))
        XCTAssertNil(TranscriptContext.title(for: try record("# AGENTS.md instructions for /project\n\n## My request:\nFix the app.")), "the daemon decides what is injected; a user message stays the person's")
    }

    func testUntitledSessionsUseTheAgentAndWorkspaceOnBothScreens() {
        var session = SessionState(id: "session-id", surface: "codex", name: "", alias: nil, status: "idle", lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil)
        session.cwd = "/projects/fieldnotes"
        XCTAssertEqual(SessionStyle.title(session), "Codex · fieldnotes")
    }
}
