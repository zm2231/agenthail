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
                self.onMessage?(type, value)
            }
        }
        let delegate = NativePeerDelegate(events: events)
        self.delegate = delegate
        guard let connection = factory.peerConnection(with: configuration, constraints: RTCMediaConstraints(mandatoryConstraints: nil, optionalConstraints: nil), delegate: delegate) else {
            throw AgenthailAPIError.unavailable("Native WebRTC could not create an audio peer.")
        }
        self.connection = connection
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

    func appendContext(_ text: String) throws {
        guard let channel, channel.readyState == .open else {
            throw AgenthailAPIError.unavailable("Voice data channel is not open.")
        }
        for data in try Self.contextFrames(text) {
            guard channel.sendData(RTCDataBuffer(data: data, isBinary: false)) else {
                throw AgenthailAPIError.unavailable("Voice data channel rejected context.")
            }
        }
    }

    static func contextFrames(_ text: String) throws -> [Data] {
        guard text.utf8.count <= 48 * 1024 else { throw AgenthailAPIError.unavailable("Voice context exceeds the frame budget.") }
        var chunks: [String] = []; var chunk = ""
        for scalar in text.unicodeScalars {
            let value = String(scalar)
            if chunk.utf8.count + value.utf8.count > 500 { chunks.append(chunk); chunk = "" }
            chunk += value
        }
        if !chunk.isEmpty { chunks.append(chunk) }
        return try chunks.map { try JSONSerialization.data(withJSONObject: ["type": "session.context.append", "channel": "commentary", "content": [["type": "input_text", "text": $0]]]) }
    }

    func end() {
        generation += 1
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
        guard !buffer.isBinary, buffer.data.count <= 64 * 1024,
              let value = try? JSONSerialization.jsonObject(with: buffer.data) as? [String: Any],
              let type = value["type"] as? String else { return }
        // Keep only event identity, never captured audio or transcript contents.
        if ["input_transcript.added", "output_transcript.added", "turn.done", "delegation.created"].contains(type) {
            events("realtime-event", type)
        }
    }
}
