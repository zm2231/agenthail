import AVFAudio
import Foundation

@MainActor
protocol VoiceAudioClient: AnyObject {
    var onMessage: ((String, String) -> Void)? { get set }
    func configureCall(id: UUID, name: String)
    func load(_ request: URLRequest)
    func start() async throws
    func end()
    func answer(_ sdp: String) async throws
    func mute(_ muted: Bool)
    func close()
}

extension VoiceAudioClient {
    func configureCall(id: UUID, name: String) {}
}

@MainActor
protocol VoicePeerClient: AnyObject {
    var onMessage: ((String, String) -> Void)? { get set }
    func start() async throws -> String
    func answer(_ sdp: String) async throws
    func mute(_ muted: Bool)
    func audioSession(_ session: AVAudioSession, active: Bool)
    func end()
}

@MainActor
final class VoiceAudioBridge: VoiceAudioClient {
    var onMessage: ((String, String) -> Void)?
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
        peer = nextPeer
        onMessage?("peer", "created")
        nextPeer.onMessage = { [weak self] type, value in
            guard let self, current == self.generation else { return }
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

    func end() {
        generation += 1
        peer?.onMessage = nil
        peer?.end(); peer = nil
        system.end()
        callID = nil
    }

    func close() { end(); permittedOrigin = false }
}
