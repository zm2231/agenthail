import AVFAudio
import Foundation

@MainActor
protocol VoiceAudioClient: AnyObject {
    var onMessage: ((String, String) -> Void)? { get set }
    var canSubmitResult: Bool { get }
    func configureCall(id: UUID, name: String)
    func load(_ request: URLRequest)
    func start() async throws
    func end()
    func answer(_ sdp: String) async throws
    func mute(_ muted: Bool)
    func appendResult(_ frame: VoiceContextFrame) throws
    func replacePeer() async throws
    func close()
}

extension VoiceAudioClient {
    var canSubmitResult: Bool { false }
    func configureCall(id: UUID, name: String) {}
    func appendResult(_ frame: VoiceContextFrame) throws { throw AgenthailAPIError.unavailable("Voice result transport is unavailable.") }
    func replacePeer() async throws { throw AgenthailAPIError.unavailable("Voice peer replacement is unavailable.") }
}

@MainActor
protocol VoicePeerClient: AnyObject {
    var onMessage: ((String, String) -> Void)? { get set }
    var canSubmitResult: Bool { get }
    func start() async throws -> String
    func answer(_ sdp: String) async throws
    func mute(_ muted: Bool)
    func appendResult(_ frame: VoiceContextFrame) throws
    func audioSession(_ session: AVAudioSession, active: Bool)
    func end()
}

extension VoicePeerClient {
    var canSubmitResult: Bool { false }
    func appendResult(_ frame: VoiceContextFrame) throws { throw AgenthailAPIError.unavailable("Voice result transport is unavailable.") }
}

@MainActor
final class VoiceAudioBridge: VoiceAudioClient {
    var onMessage: ((String, String) -> Void)?
    var canSubmitResult: Bool { peer?.canSubmitResult == true }
    private let system: any VoiceSystemCalling
    private let makePeer: () -> any VoicePeerClient
    private let permission: () async -> Bool
    private var peer: (any VoicePeerClient)?
    private var generation = 0
    private var callID: UUID?
    private var name = "Agenthail orchestrator"
    private var permittedOrigin = false
    private var muted = false
    private var held = false
    private var peerGeneration = 0
    private var activation: (AVAudioSession, Bool)?

    init(system: any VoiceSystemCalling = VoiceSystemCall(),
         makePeer: @escaping () -> any VoicePeerClient = { NativeVoicePeer() },
         permission: @escaping () async -> Bool = {
             await withCheckedContinuation { continuation in
                 AVAudioApplication.requestRecordPermission { continuation.resume(returning: $0) }
             }
         }) {
        self.system = system
        self.makePeer = makePeer
        self.permission = permission
    }

    func configureCall(id: UUID, name: String) { callID = id; self.name = name }

    func load(_ request: URLRequest) {
        permittedOrigin = request.url?.scheme == "https" && request.url?.host != nil
        onMessage?(permittedOrigin ? "ready" : "unavailable", permittedOrigin ? "" : "Pair with the Mac's secure HTTPS address first.")
    }

    func start() async throws {
        guard permittedOrigin else { throw AgenthailAPIError.unavailable("Voice requires a secure paired Mac.") }
        generation += 1
        let current = generation
        let id = callID ?? UUID()
        muted = false; held = false
        let allowed = await permission()
        guard current == generation else { throw CancellationError() }
        onMessage?("permission", allowed ? "granted" : "denied")
        guard allowed else { throw AgenthailAPIError.unavailable("Microphone access is off. Enable it for Agenthail in Settings.") }
        let nextPeer = makePeer()
        peerGeneration += 1
        let media = peerGeneration
        peer = nextPeer
        onMessage?("peer", "created")
        nextPeer.onMessage = { [weak self] type, value in
            guard let self, current == self.generation, media == self.peerGeneration else { return }
            if type == "connection", value == "connected" { self.system.connected() }
            self.onMessage?(type, value)
        }
        system.onEnd = { [weak self] cause in
            guard let self, current == self.generation else { return }
            self.end()
            self.onMessage?("ended", cause)
        }
        system.onMute = { [weak self] muted in
            guard let self, current == self.generation else { return }
            self.muted = muted
            self.peer?.mute(muted || self.held)
            self.onMessage?("mute", muted ? "true" : "false")
        }
        system.onHold = { [weak self] held in
            guard let self, current == self.generation else { return }
            self.held = held
            self.peer?.mute(held || self.muted)
            self.onMessage?("held", held ? "true" : "false")
        }
        system.onActivation = { [weak self] session, active in
            guard let self, current == self.generation else { return }
            self.peer?.audioSession(session, active: active)
            self.activation = (session, active)
            if active { self.onMessage?("activation", "") }
            self.onMessage?("suspended", active ? "false" : "true")
        }
        do {
            try await system.start(id: id, name: name)
            guard current == generation else { throw CancellationError() }
            let sdp = try await nextPeer.start()
            guard current == generation else { throw CancellationError() }
            onMessage?("offer", sdp)
        } catch {
            if current == generation { end() }
            throw error
        }
    }

    func answer(_ sdp: String) async throws {
        guard let peer else { throw AgenthailAPIError.unavailable("No pending native voice connection.") }
        try await peer.answer(sdp)
    }

    func mute(_ muted: Bool) { self.muted = muted; peer?.mute(muted || held); system.mute(muted) }

    func appendResult(_ frame: VoiceContextFrame) throws {
        guard let peer else { throw AgenthailAPIError.unavailable("No active native voice connection.") }
        try peer.appendResult(frame)
    }

    func replacePeer() async throws {
        let current = generation
        peerGeneration += 1
        let media = peerGeneration
        peer?.onMessage = nil
        peer?.end()
        let next = makePeer()
        peer = next
        next.onMessage = { [weak self] type, value in
            guard let self, current == self.generation, media == self.peerGeneration else { return }
            self.onMessage?(type, value)
        }
        if let (session, active) = activation { next.audioSession(session, active: active) }
        next.mute(muted || held)
        let offer = try await next.start()
        guard current == generation, media == peerGeneration else { throw CancellationError() }
        onMessage?("offer", offer)
    }

    func end() {
        generation += 1
        peerGeneration += 1
        peer?.onMessage = nil
        peer?.end(); peer = nil
        system.end()
        callID = nil
        activation = nil
    }

    func close() { end(); permittedOrigin = false }
}
