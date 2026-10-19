import AVFAudio
import XCTest
@testable import Agenthail

@MainActor
final class NativeVoiceAudioTests: XCTestCase {
    func testWebRTCLicenseIsIncludedInApplicationBundle() throws {
        let file = try XCTUnwrap(Bundle.main.url(forResource: "WebRTC-LICENSE", withExtension: "txt"))
        XCTAssertTrue(try String(contentsOf: file, encoding: .utf8).contains("The WebRTC project authors"))
    }

    func testMediaWaitsForSystemActivationAndUsesHostCallUUID() async throws {
        let system = SystemCallFixture(); let peer = VoicePeerFixture()
        let bridge = NativeVoiceAudio(system: system, makePeer: { peer }, permission: { true })
        let callID = UUID()
        bridge.configureCall(id: callID, name: "Test session")
        bridge.load(URLRequest(url: URL(string: "https://paired.test/api/v1/voice")!))
        var offers: [String] = []
        bridge.onMessage = { type, value in if type == "offer" { offers.append(value) } }
        let start = Task { try await bridge.start() }
        for _ in 0..<1000 where system.pending == nil { await Task.yield() }
        XCTAssertEqual(system.callID, callID)
        XCTAssertEqual(system.name, "Test session")
        XCTAssertEqual(peer.starts, 0)
        system.activate()
        try await start.value
        XCTAssertEqual(peer.starts, 1)
        XCTAssertEqual(peer.activations, [true])
        XCTAssertEqual(offers, ["v=0 native-fixture"])
        try await bridge.answer("v=0 answer")
        XCTAssertEqual(peer.answers, ["v=0 answer"])
        bridge.end()
        XCTAssertEqual(peer.ends, 1)
        XCTAssertEqual(system.ends, 1)
    }

    func testHangupDuringSystemActivationCannotStartMediaOrEmitOffer() async {
        let system = SystemCallFixture(); let peer = VoicePeerFixture()
        let bridge = NativeVoiceAudio(system: system, makePeer: { peer }, permission: { true })
        bridge.load(URLRequest(url: URL(string: "https://paired.test")!))
        var offers = 0
        bridge.onMessage = { type, _ in if type == "offer" { offers += 1 } }
        let start = Task { try await bridge.start() }
        for _ in 0..<1000 where system.pending == nil { await Task.yield() }
        let staleMute = system.onMute
        bridge.end()
        system.activate()
        do { try await start.value; XCTFail("cancelled call started") } catch {}
        staleMute?(true)
        XCTAssertEqual(peer.starts, 0)
        XCTAssertEqual(offers, 0)
        XCTAssertTrue(peer.mutes.isEmpty)
    }

    func testHangupDuringPermissionCannotRegisterASystemCall() async {
        let system = SystemCallFixture(); let peer = VoicePeerFixture()
        var permission: CheckedContinuation<Bool, Never>?
        let bridge = NativeVoiceAudio(system: system, makePeer: { peer }, permission: { await withCheckedContinuation { permission = $0 } })
        bridge.load(URLRequest(url: URL(string: "https://paired.test")!))
        let start = Task { try await bridge.start() }
        for _ in 0..<1000 where permission == nil { await Task.yield() }
        bridge.end()
        permission?.resume(returning: true)
        do { try await start.value; XCTFail("cancelled permission started a call") } catch {}
        XCTAssertNil(system.callID)
        XCTAssertEqual(peer.starts, 0)
    }

    func testSystemHoldPreservesUserMuteAndReportsAudioSuspension() async throws {
        let system = SystemCallFixture(); let peer = VoicePeerFixture()
        let bridge = NativeVoiceAudio(system: system, makePeer: { peer }, permission: { true })
        bridge.load(URLRequest(url: URL(string: "https://paired.test")!))
        var suspensions: [String] = []
        bridge.onMessage = { type, value in if type == "suspended" { suspensions.append(value) } }
        let start = Task { try await bridge.start() }
        for _ in 0..<1000 where system.pending == nil { await Task.yield() }
        system.activate(); try await start.value
        bridge.mute(true)
        system.onHold?(true)
        system.onActivation?(AVAudioSession.sharedInstance(), false)
        system.onHold?(false)
        XCTAssertEqual(peer.mutes, [true, true, true])
        XCTAssertEqual(peer.activations, [true, false])
        XCTAssertEqual(suspensions, ["false", "true"])
        bridge.end()
    }

    func testDeniedPermissionDoesNotCreatePeerOrCall() async {
        let system = SystemCallFixture(); let peer = VoicePeerFixture()
        let bridge = NativeVoiceAudio(system: system, makePeer: { peer }, permission: { false })
        bridge.load(URLRequest(url: URL(string: "https://paired.test")!))
        do { try await bridge.start(); XCTFail("denied microphone permission accepted") } catch {}
        XCTAssertNil(system.callID)
        XCTAssertEqual(peer.starts, 0)
    }

    func testSystemHangupStopsMediaWithoutAViewOrMessageListener() async throws {
        let system = SystemCallFixture(); let peer = VoicePeerFixture()
        let bridge = NativeVoiceAudio(system: system, makePeer: { peer }, permission: { true })
        bridge.load(URLRequest(url: URL(string: "https://paired.test")!))
        let start = Task { try await bridge.start() }
        for _ in 0..<1000 where system.pending == nil { await Task.yield() }
        system.activate(); try await start.value
        let staleActivation = system.onActivation
        system.onEnd?("user_hangup")
        XCTAssertEqual(peer.ends, 1)
        XCTAssertNil(peer.onMessage)
        staleActivation?(AVAudioSession.sharedInstance(), true)
        XCTAssertEqual(peer.activations, [true])
    }

    func testSimulatorCallActivatesVoiceAudioWithoutCallKitAndReleasesItOnHangup() async throws {
        let peer = VoicePeerFixture()
        let bridge = NativeVoiceAudio(makePeer: { peer }, permission: { true })
        bridge.load(URLRequest(url: URL(string: "https://paired.test/api/v1/voice")!))
        var messages: [String] = []
        bridge.onMessage = { type, value in messages.append("\(type):\(value)") }
        try await bridge.start()
        XCTAssertEqual(peer.activations, [true])
        XCTAssertEqual(AVAudioSession.sharedInstance().category, .playAndRecord)
        XCTAssertEqual(AVAudioSession.sharedInstance().mode, .voiceChat)
        XCTAssertTrue(messages.contains("offer:v=0 native-fixture"))
        bridge.mute(true)
        XCTAssertEqual(peer.mutes.last, true)
        bridge.end()
        XCTAssertEqual(peer.ends, 1)
        try await bridge.start()
        XCTAssertEqual(peer.starts, 2)
        bridge.end()
    }

    func testNativeSDKProducesAudioAndDataOfferWithoutActivatingMicrophone() async throws {
        let peer = NativeVoicePeer()
        defer { peer.end() }
        let offer = try await peer.start()
        XCTAssertTrue(offer.hasPrefix("v=0"))
        XCTAssertTrue(offer.contains("m=audio"))
        XCTAssertTrue(offer.contains("m=application"))
        XCTAssertFalse(offer.contains("m=video"))
    }
}

@MainActor
private final class SystemCallFixture: VoiceSystemCalling {
    var onEnd: ((String) -> Void)?
    var onMute: ((Bool) -> Void)?
    var onHold: ((Bool) -> Void)?
    var onActivation: ((AVAudioSession, Bool) -> Void)?
    var pending: CheckedContinuation<Void, any Error>?
    var callID: UUID?
    var name: String?
    var ends = 0
    func start(id: UUID, name: String) async throws {
        callID = id; self.name = name
        try await withCheckedThrowingContinuation { pending = $0 }
    }
    func activate() {
        guard let pending else { return }
        self.pending = nil
        onActivation?(AVAudioSession.sharedInstance(), true)
        pending.resume()
    }
    func connected() {}
    func end() {
        ends += 1
        let pending = pending; self.pending = nil
        pending?.resume(throwing: CancellationError())
    }
    func mute(_ muted: Bool) {}
}

@MainActor
private final class VoicePeerFixture: VoicePeerClient {
    var onMessage: ((String, String) -> Void)?
    var starts = 0
    var ends = 0
    var activations: [Bool] = []
    var answers: [String] = []
    var mutes: [Bool] = []
    func start() async throws -> String { starts += 1; return "v=0 native-fixture" }
    func answer(_ sdp: String) async throws { answers.append(sdp) }
    func mute(_ muted: Bool) { mutes.append(muted) }
    func audioSession(_ session: AVAudioSession, active: Bool) { activations.append(active) }
    func end() { ends += 1 }
}
