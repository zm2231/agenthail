import Observation
import UserNotifications
import XCTest
@testable import Agenthail

@MainActor
final class PushRegistrarTests: XCTestCase {
    private let suite = "agenthail-tests-push-registrar"

    override func setUp() {
        KeychainStore.removeAll()
        UserDefaults().removePersistentDomain(forName: suite)
        PushRecordingProtocol.log.reset()
        PushRecordingProtocol.challengeStatus = 404
    }

    override func tearDown() {
        PushRecordingProtocol.challengeStatus = 404
        KeychainStore.removeAll()
        UserDefaults().removePersistentDomain(forName: suite)
    }

    func testSettingsSeesTheStatusChangeWhenADeviceTokenArrives() async throws {
        PushRecordingProtocol.challengeStatus = 503
        let model = try await connectedModel()
        defer { model.forgetHost("hmac") }
        let changed = expectation(description: "the status Settings shows is observed through the model")
        withObservationTracking { _ = model.push.notificationStatus } onChange: { changed.fulfill() }
        model.push.registerPushToken("a1b2c3")
        await fulfillment(of: [changed], timeout: 1)
        XCTAssertEqual(model.push.notificationStatus, "Enabling")
    }

    func testAComputerRowFollowsItsSetupAndForgetsItWithTheComputer() async throws {
        let model = try await connectedModel()
        model.push.registerPushToken("a1b2c3")
        XCTAssertEqual(model.hosts.first?.notifications, "Enabling", "the row shows setup starting")
        for _ in 0..<300 where model.hosts.first?.notifications == "Enabling" {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertEqual(model.hosts.first?.notifications, "Setup failed", "a refused registration reaches the row")
        XCTAssertEqual(model.push.notificationStatus, "Setup failed")
        model.forgetHost("hmac")
        XCTAssertTrue(model.hosts.isEmpty)
        XCTAssertEqual(model.push.status(for: "hmac"), "Not enabled", "a forgotten computer leaves no push status behind")
    }

    func testGoingToTheBackgroundStopsAWaitingRetry() async throws {
        let model = try await retrying()
        defer { model.forgetHost("hmac") }
        model.setAppActive(false)
        XCTAssertEqual(model.push.status(for: "hmac"), "Reconnect to finish setup")
        try await assertNoMoreRelayAttempts()
    }

    func testAnUnreachableComputerStopsItsWaitingRetry() async throws {
        let model = try await retrying()
        defer { model.forgetHost("hmac") }
        model.push.hostUnreachable("hmac")
        XCTAssertEqual(model.push.status(for: "hmac"), "Reconnect to finish setup")
        try await assertNoMoreRelayAttempts()
    }

    func testForgettingAComputerStopsItsWaitingRetry() async throws {
        let model = try await retrying()
        model.forgetHost("hmac")
        XCTAssertEqual(model.push.status(for: "hmac"), "Not enabled")
        try await assertNoMoreRelayAttempts()
    }

    func testPermissionFoundMissingWhileSetupWaitsStopsItAndTellsTheComputer() async throws {
        let answer = AuthorizationAnswer()
        PushRecordingProtocol.challengeStatus = 503
        let model = try await connectedModel { answer.status }
        defer { model.forgetHost("hmac") }
        model.push.registerPushToken("a1b2c3")
        for _ in 0..<300 where !PushRecordingProtocol.log.contains("POST /v1/attest/challenge") {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(PushRecordingProtocol.log.contains("POST /v1/attest/challenge"), "setup reached the relay")
        XCTAssertEqual(model.push.status(for: "hmac"), "Enabling", "setup is waiting on a busy relay")
        answer.status = .notDetermined
        model.push.refreshNotificationRegistration()
        for _ in 0..<300 where model.push.status(for: "hmac") != "Not enabled" {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertEqual(model.push.status(for: "hmac"), "Not enabled", "without permission the computer shows notifications off")
        XCTAssertTrue(PushRecordingProtocol.log.contains("DELETE /api/v1/device/push"), "the computer is told not to send alerts")
        try await assertNoMoreRelayAttempts()
    }

    private func retrying() async throws -> AgenthailIOSModel {
        PushRecordingProtocol.challengeStatus = 503
        let model = try await connectedModel()
        model.push.registerPushToken("a1b2c3")
        for _ in 0..<300 where !PushRecordingProtocol.log.contains("POST /v1/attest/challenge") {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertTrue(PushRecordingProtocol.log.contains("POST /v1/attest/challenge"), "setup reached the relay")
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertEqual(model.push.status(for: "hmac"), "Enabling", "a busy relay leaves setup waiting to retry")
        return model
    }

    private func assertNoMoreRelayAttempts() async throws {
        let attempts = PushRecordingProtocol.log.all.filter { $0 == "POST /v1/attest/challenge" }.count
        try await Task.sleep(for: .seconds(6))
        XCTAssertEqual(PushRecordingProtocol.log.all.filter { $0 == "POST /v1/attest/challenge" }.count, attempts, "the stopped retry never reaches the relay again")
    }

    private func connectedModel(authorization: @escaping () async -> UNAuthorizationStatus = { .authorized }) async throws -> AgenthailIOSModel {
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        try PairedCredential.save(PairedHostCredential(hostID: "hmac", hostName: "mac", endpoint: URL(string: "https://mac.tailnet.ts.net")!, token: "token", pairedAt: Date()))
        let model = AgenthailIOSModel(autoConnect: false, session: FixtureScope.open(self).session([PushRecordingProtocol.self]), drops: nil, notificationDefaults: defaults, notificationAuthorization: authorization)
        model.resumeConnection()
        for _ in 0..<300 where model.hosts.first?.reachable != true {
            try await Task.sleep(for: .milliseconds(10))
        }
        XCTAssertEqual(model.hosts.first?.reachable, true, "the computer connects")
        return model
    }
}

private final class AuthorizationAnswer {
    var status = UNAuthorizationStatus.authorized
}
