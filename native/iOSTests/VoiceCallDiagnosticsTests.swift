import XCTest
@testable import Agenthail

@MainActor
final class VoiceCallDiagnosticsTests: XCTestCase {
    func testFailureStoresStageAndCodeWithoutPrivateErrorText() throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let file = directory.appendingPathComponent("attempts.json")
        let log = VoiceCallDiagnostics(file: file)
        log.begin(id: "failure")
        let error = NSError(domain: NSURLErrorDomain, code: NSURLErrorTimedOut, userInfo: [NSLocalizedDescriptionKey: "private transcript and token"])
        log.failure(error, stage: "native_audio_start", id: "failure")
        log.end("native_audio_start_failed", id: "failure")
        let reloaded = VoiceCallDiagnostics(file: file)
        XCTAssertEqual(reloaded.attempts.last?.failureStage, "native_audio_start")
        XCTAssertEqual(reloaded.attempts.last?.failureCode, NSURLErrorTimedOut)
        XCTAssertEqual(reloaded.attempts.last?.failureDomain, NSURLErrorDomain)
        XCTAssertFalse(String(decoding: try Data(contentsOf: file), as: UTF8.self).contains("private transcript"))
    }
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

    func testRecordsAreBoundedAndDuplicateMilestonesKeepFirstTime() {
        let log = VoiceCallDiagnostics(file: nil)
        for index in 0..<25 { log.begin(id: String(index)) }
        XCTAssertEqual(log.attempts.count, 20)
        log.mark("activation", id: "24")
        let first = log.attempts.last?.milestones["activation"]
        log.mark("activation", id: "24")
        XCTAssertEqual(log.attempts.last?.milestones["activation"], first)
    }
}
