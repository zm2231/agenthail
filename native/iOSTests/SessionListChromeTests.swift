import XCTest
@testable import Agenthail

@MainActor
final class SessionListChromeTests: XCTestCase {
    private func session(status: String = "idle") -> SessionState {
        SessionState(id: "s", surface: "claude", name: "s", alias: nil, status: status, lastActive: nil, queueCount: 0, open: true, current: true, currentReason: nil, capabilities: Capabilities(), readOnly: false, readOnlyReason: nil)
    }

    func testTheRingNamesEachStatusOnceInPriorityOrder() {
        var waiting = session(status: "busy")
        waiting.waitingOn = WaitingState(kind: "question", id: "q")
        XCTAssertEqual(SessionRing.kind(waiting, needsYou: false), .needsYou, "a question beats working")

        var stranded = session(status: "busy")
        stranded.host = SessionHost(id: "h", name: "build-server", state: "unreachable")
        stranded.unavailableReason = "build-server is offline"
        XCTAssertEqual(SessionRing.kind(stranded, needsYou: false), .unavailable)
        XCTAssertEqual(SessionRing.kind(stranded, needsYou: true), .needsYou, "a delivery problem still needs the person")

        XCTAssertEqual(SessionRing.kind(session(status: "busy"), needsYou: false), .working)

        var background = session()
        background.backgroundTasks = BackgroundTasks(running: true, source: "transcript")
        XCTAssertEqual(SessionRing.kind(background, needsYou: false), .background)

        XCTAssertEqual(SessionRing.kind(session(), needsYou: false), .idle)

        var unreachableQuestion = waiting
        unreachableQuestion.host = SessionHost(id: "h", name: "build-server", state: "unreachable")
        XCTAssertEqual(SessionRing.kind(unreachableQuestion, needsYou: false), .unavailable, "a question on a computer out of reach cannot be answered, as the daemon says")
        var busyFinishing = session(status: "busy")
        busyFinishing.backgroundTasks = BackgroundTasks(running: true, source: "transcript")
        XCTAssertEqual(SessionRing.kind(busyFinishing, needsYou: false), .working)
        var staleReason = session()
        staleReason.host = SessionHost(id: "h", name: "build-server", state: "connected")
        staleReason.unavailableReason = "build-server is offline"
        XCTAssertEqual(SessionRing.kind(staleReason, needsYou: false), .idle, "a connected computer's leftover reason does not make the row unavailable")
    }

    func testEveryRingHasWordsAndIdleHasNone() {
        for ring in [SessionRing.needsYou, .unavailable, .working, .background] {
            XCTAssertFalse((ring.spoken ?? "").isEmpty, "\(ring) must be spoken, not only drawn")
        }
        XCTAssertNil(SessionRing.idle.spoken)
    }

    func testEachAgentHasItsOwnGlyph() {
        XCTAssertNotEqual(SessionAvatar.glyph("claude"), SessionAvatar.glyph("codex"))
        XCTAssertEqual(SessionAvatar.glyph("notion"), SessionAvatar.glyph("other"))
        for surface in ["claude", "codex", "notion"] {
            XCTAssertNotNil(UIImage(systemName: SessionAvatar.glyph(surface)), surface)
        }
    }
}
