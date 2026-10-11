import AVFAudio
import Foundation
@preconcurrency import WebRTC

@MainActor
final class NativeVoicePeer: VoicePeerClient {
    var onMessage: ((String, String) -> Void)?
    private lazy var factory = RTCPeerConnectionFactory()
    private var connection: RTCPeerConnection?
    private var channel: RTCDataChannel?
    private var microphone: RTCAudioTrack?
    private var delegate: NativePeerDelegate?
    private var channelDelegate: NativeChannelDelegate?
    private var generation = 0
    private var muted = false
    private var speakablePending = false
    private var playbackMonitor: Task<Void, Never>?
    var canSubmitResult: Bool { !speakablePending && playback.canPublish }
    private lazy var playback = VoicePlayback { [weak self] suppressed in
        for transceiver in self?.connection?.transceivers ?? [] {
            (transceiver.receiver.track as? RTCAudioTrack)?.isEnabled = !suppressed
        }
    }

    init() {
        let audio = RTCAudioSession.sharedInstance()
        audio.useManualAudio = true
        audio.isAudioEnabled = false
    }

    func audioSession(_ session: AVAudioSession, active: Bool) {
        let audio = RTCAudioSession.sharedInstance()
        if active { audio.audioSessionDidActivate(session) }
        else { audio.audioSessionDidDeactivate(session) }
        audio.isAudioEnabled = active
    }

    func start() async throws -> String {
        generation += 1
        let current = generation
        let configuration = RTCConfiguration()
        configuration.sdpSemantics = .unifiedPlan
        configuration.continualGatheringPolicy = .gatherContinually
        let events: @Sendable (String, String) -> Void = { [weak self] type, value in
            Task { @MainActor in
                guard let self, current == self.generation else { return }
                if type == "realtime-event" { self.observe(value) }
                self.onMessage?(type, value)
            }
        }
        let delegate = NativePeerDelegate(events: events)
        self.delegate = delegate
        guard let connection = factory.peerConnection(with: configuration, constraints: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil), delegate: delegate) else {
            throw AgenthailAPIError.unavailable("Native WebRTC could not create an audio peer.")
        }
        self.connection = connection
        playbackMonitor = Task { [weak self, weak connection] in
            var previousEnergy = 0.0
            while !Task.isCancelled {
                guard let self, let connection, current == self.generation else { return }
                let energy: Double = await withCheckedContinuation { continuation in
                    connection.statistics { report in
                        let total = report.statistics.values.reduce(0.0) { sum, stat in
                            guard stat.type == "inbound-rtp", (stat.values["kind"] as? NSString) == "audio" else { return sum }
                            return sum + ((stat.values["totalAudioEnergy"] as? NSNumber)?.doubleValue ?? 0)
                        }
                        continuation.resume(returning: total)
                    }
                }
                guard !Task.isCancelled, current == self.generation else { return }
                if energy > previousEnergy { self.playback.audioActivity() }
                previousEnergy = energy
                try? await Task.sleep(for: .milliseconds(100))
            }
        }
        let source = factory.audioSource(with: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil))
        let track = factory.audioTrack(with: source, trackId: "agenthail-microphone")
        track.isEnabled = !muted
        microphone = track
        connection.add(track, streamIds: ["agenthail-audio"])
        let dataConfiguration = RTCDataChannelConfiguration()
        guard let channel = connection.dataChannel(forLabel: "oai-events", configuration: dataConfiguration) else {
            throw AgenthailAPIError.unavailable("Native WebRTC could not create the voice data channel.")
        }
        self.channel = channel
        channelDelegate = NativeChannelDelegate(events: events)
        channel.delegate = channelDelegate
        let offer: RTCSessionDescription = try await withCheckedThrowingContinuation { continuation in
            connection.offer(for: RTCMediaConstraints(mandatoryConstraints: ["OfferToReceiveAudio": "true", "OfferToReceiveVideo": "false"], optionalConstraints: nil)) { sdp, error in
                if let error { continuation.resume(throwing: error) }
                else if let sdp { continuation.resume(returning: sdp) }
                else { continuation.resume(throwing: AgenthailAPIError.invalidResponse) }
            }
        }
        guard current == generation else { throw CancellationError() }
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
            connection.setLocalDescription(offer) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
        guard current == generation else { throw CancellationError() }
        return connection.localDescription?.sdp ?? offer.sdp
    }

    func answer(_ sdp: String) async throws {
        guard let connection else { throw AgenthailAPIError.unavailable("Native WebRTC has no pending offer.") }
        let current = generation
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, any Error>) in
            connection.setRemoteDescription(RTCSessionDescription(type: .answer, sdp: sdp)) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
        guard current == generation else { throw CancellationError() }
    }

    func mute(_ muted: Bool) {
        self.muted = muted
        microphone?.isEnabled = !muted
        onMessage?("input-muted", muted ? "true" : "false")
    }

    func appendResult(_ frame: VoiceContextFrame) throws {
        guard let channel, channel.readyState == .open else {
            throw AgenthailAPIError.unavailable("Voice data channel is not open.")
        }
        for data in try Self.resultFrames(frame) {
            guard channel.sendData(RTCDataBuffer(data: data, isBinary: false)) else {
                throw AgenthailAPIError.unavailable("Voice data channel rejected context.")
            }
        }
        speakablePending = true
    }

    static func resultFrames(_ frame: VoiceContextFrame) throws -> [Data] {
        guard frame.channel == "speakable", frame.text.utf8.count <= 16 * 1024 else { throw AgenthailAPIError.unavailable("Invalid voice delegation result.") }
        var payload: [String: Any] = ["channel": frame.channel, "content": [["type": "input_text", "text": frame.text]]]
        switch frame.kind {
        case "delegation-result":
            guard !frame.delegationId.isEmpty else { throw AgenthailAPIError.unavailable("Missing voice delegation identity.") }
            payload["type"] = "delegation.context.append"
            payload["delegation_item_id"] = frame.delegationId
        case "typed-result":
            guard frame.delegationId.isEmpty else { throw AgenthailAPIError.unavailable("Unexpected typed delegation identity.") }
            payload["type"] = "session.context.append"
        default: throw AgenthailAPIError.unavailable("Unsupported voice result origin.")
        }
        let data = try JSONSerialization.data(withJSONObject: payload)
        guard data.count <= 64 * 1024 else { throw AgenthailAPIError.unavailable("Voice result exceeds the frame budget.") }
        return [data]
    }

    private func observe(_ raw: String) {
        guard let data = raw.data(using: .utf8), let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }
        switch event["type"] as? String {
        case "input_transcript.added":
            if !muted, Self.transcriptText(event) != nil { playback.inputStarted(speechPending: speakablePending) }
        case "output_transcript.added":
            if Self.transcriptText(event) != nil { playback.outputAdded() }
        case "turn.done":
            guard let turn = event["turn"] as? [String: Any] else { return }
            if turn["role"] as? String == "user", !muted {
                playback.inputFinished(speechPending: speakablePending, hasTranscript: !(turn["transcript"] as? String ?? "").isEmpty)
            } else if turn["role"] as? String == "assistant" {
                playback.outputFinished(); speakablePending = false
            }
        default: break
        }
    }

    nonisolated private static func transcriptText(_ event: [String: Any]) -> String? {
        guard let item = event["item"] as? [String: Any], let text = item["text"] as? String,
              !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return text
    }

    nonisolated static func providerEvent(_ data: Data) -> (String, String)? {
        guard data.count <= 64 * 1024,
              let event = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let type = event["type"] as? String else { return nil }
        if type == "error" { return ("error", "The voice provider rejected a call event. Reconnect before repeating an action.") }
        guard ["input_transcript.added", "output_transcript.added", "turn.done", "delegation.created"].contains(type),
              let raw = String(data: data, encoding: .utf8) else { return nil }
        return ("realtime-event", raw)
    }

    func end() {
        generation += 1
        playbackMonitor?.cancel(); playbackMonitor = nil
        microphone?.isEnabled = false
        channel?.delegate = nil
        channel?.close()
        connection?.close()
        RTCAudioSession.sharedInstance().isAudioEnabled = false
        microphone = nil; channel = nil; connection = nil
        delegate = nil; channelDelegate = nil
    }
}

private final class NativePeerDelegate: NSObject, RTCPeerConnectionDelegate, @unchecked Sendable {
    private let events: @Sendable (String, String) -> Void
    init(events: @escaping @Sendable (String, String) -> Void) { self.events = events }
    func peerConnection(_ peerConnection: RTCPeerConnection, didChange stateChanged: RTCSignalingState) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didAdd stream: RTCMediaStream) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didRemove stream: RTCMediaStream) {}
    func peerConnectionShouldNegotiate(_ peerConnection: RTCPeerConnection) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCIceConnectionState) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCIceGatheringState) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didGenerate candidate: RTCIceCandidate) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didRemove candidates: [RTCIceCandidate]) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didOpen dataChannel: RTCDataChannel) {}
    func peerConnection(_ peerConnection: RTCPeerConnection, didChange newState: RTCPeerConnectionState) {
        let value: String
        switch newState {
        case .new: value = "new"
        case .connecting: value = "connecting"
        case .connected: value = "connected"
        case .disconnected: value = "disconnected"
        case .failed: value = "failed"
        case .closed: value = "closed"
        @unknown default: value = "failed"
        }
        events("connection", value)
    }
}

private final class NativeChannelDelegate: NSObject, RTCDataChannelDelegate, @unchecked Sendable {
    private let events: @Sendable (String, String) -> Void
    init(events: @escaping @Sendable (String, String) -> Void) { self.events = events }
    func dataChannelDidChangeState(_ dataChannel: RTCDataChannel) {
        if dataChannel.readyState == .open { events("channel", "open") }
        if dataChannel.readyState == .closed { events("error", "The voice data channel closed.") }
    }
    func dataChannel(_ dataChannel: RTCDataChannel, didReceiveMessageWith buffer: RTCDataBuffer) {
        guard !buffer.isBinary, let (type, value) = NativeVoicePeer.providerEvent(buffer.data) else { return }
        events(type, value)
    }
}
