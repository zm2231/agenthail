import XCTest
@testable import Agenthail

@MainActor
final class VoiceCallDiagnosticsTests: XCTestCase {
    func testFirstTerminalCauseSurvivesCleanupAndRecordReload() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("attempts.json")
        let log = VoiceCallDiagnostics(file: file)
        log.begin(id: "call-a")
        log.mark("peer_connected", id: "call-a")
        log.end("peer_failed", id: "call-a")
        log.end("user_hangup", id: "call-a")
        XCTAssertNil(log.persistenceError)
        let reloaded = VoiceCallDiagnostics(file: file)
        XCTAssertEqual(reloaded.attempts.first?.terminalCause, "peer_failed")
        XCTAssertNotNil(reloaded.attempts.first?.milestones["peer_connected"])
        XCTAssertNotNil(reloaded.attempts.first?.milestones["local_ended"])
    }

    func testRelaunchClassifiesUnfinishedAttemptWithoutClaimingConnection() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("attempts.json")
        let log = VoiceCallDiagnostics(file: file)
        log.begin(id: "call-a")
        log.mark("peer_connected", id: "call-a")
        let reloaded = VoiceCallDiagnostics(file: file)
        XCTAssertEqual(reloaded.attempts.first?.terminalCause, "process_terminated_or_unknown")
    }
}
