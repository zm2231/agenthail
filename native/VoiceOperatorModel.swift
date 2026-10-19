import Foundation
import SwiftUI

@MainActor
final class VoiceOperatorModel: ObservableObject {
    @Published var state: VoiceState?
    @Published var detail: SessionDetail?
    @Published var error: String?
    @Published var activityError: String?
    @Published var ready = false
    @Published var working = false
    @Published var dialing = false
    @Published var connectionError: String?
    @Published var muted = false
    @Published private(set) var microphoneDenied = false
    @Published var audioConnected = false
    @Published private(set) var hostHangupPending = false
    @Published private(set) var hostHangupUnconfirmed = false
    @Published var text = ""
    @Published var targetID = ""
    let isPreview: Bool
    let audio: any VoiceAudioClient
    private var api: (any VoiceServiceClient)?
    private var timelineAPI: AgenthailAPI?
    private var polling: Task<Void, Never>?
    private var generation = 0
    private var attemptID: String?
    private var appliedSDP: String?
    private var channelOpen = false
    private var connectedReported = false
    private var startSubmitted = false
    private var hostHangupAttemptID: String?
    private var closed = false
    var canCall: Bool { ready && !working && !dialing && !audioConnected && !hostHangupPending && !hostHangupUnconfirmed && !closed && state?.hasCall != true && state?.phase != "blocked" && connectionError == nil }
    var canStartNewConversation: Bool {
        state != nil && !working && !dialing && !audioConnected && !hostHangupPending && !hostHangupUnconfirmed
            && !closed && state?.hasCall != true && state?.occupied != true
            && state?.phase != "creating" && state?.phase != "blocked"
    }

    init(preview: Bool = false, targetID: String = "", api: (any VoiceServiceClient)? = nil, timelineAPI: AgenthailAPI? = nil, audio: (any VoiceAudioClient)? = nil, unavailable: String? = nil) {
        isPreview = preview
        self.targetID = targetID
        self.audio = audio ?? VoiceAudioBridge()
#if DEBUG
        if preview {
            state = try? JSONDecoder().decode(VoiceState.self, from: Data(Self.previewState.utf8))
            ready = true
            return
        }
#endif
        self.api = api
        self.timelineAPI = timelineAPI
        error = unavailable
        self.audio.onMessage = { [weak self] type, value in self?.receive(type, value) }
    }

    convenience init(endpoint: URL, token: String, targetID: String = "") {
        let api: VoiceAPI?
        var failure: String?
        do { api = try VoiceAPI(endpoint: endpoint, token: token) }
        catch { api = nil; failure = error.localizedDescription }
        self.init(targetID: targetID, api: api, timelineAPI: api == nil ? nil : AgenthailAPI(baseURL: endpoint, token: token), unavailable: failure)
    }

#if DEBUG
    private static var previewState: String {
        var events: [[String: Any]] = []
        for sequence in 1...10 {
            events.append([
                "sequence": sequence,
                "method": "thread/realtime/transcript/done",
                "params": ["role": sequence.isMultiple(of: 2) ? "assistant" : "user",
                           "text": sequence == 10 ? "Newest voice update" : "Conversation update \(sequence). This keeps enough history on screen to exercise follow-latest behavior."],
            ])
        }
        let value: [String: Any] = ["protocol": 1, "phase": "ready", "events": events, "truncated": false, "occupied": false]
        return String(data: try! JSONSerialization.data(withJSONObject: value), encoding: .utf8)!
    }
#endif

    func open() {
        guard let api, polling == nil, !closed else { return }
        audio.load(api.request(path: "api/v1/voice/peer"))
        polling = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }

    func refresh() async {
        guard let api, !closed else { return }
        let current = generation
        do {
            let next = try await api.state()
            guard !closed, current == generation else { return }
            let terminal = next.phase == "ended" || next.phase == "blocked"
            if terminal, (hostHangupPending || hostHangupUnconfirmed), next.attemptId != hostHangupAttemptID { return }
            if terminal, dialing || audioConnected {
                guard let attemptID, next.attemptId == attemptID else { return }
                if error == nil {
                    let fallback: String
                    if next.phase == "blocked" { fallback = "The host blocked this voice call. Check Voice details before trying again." }
                    else if dialing { fallback = "The host ended this voice call before audio connected. Check Voice details before trying again." }
                    else { fallback = "The host ended this connected voice call. Check Voice details before trying again." }
                    error = next.message.flatMap { $0.isEmpty ? nil : $0 } ?? fallback
                }
            }
            state = next
            connectionError = nil
            if terminal {
                let hadLocalCall = dialing || audioConnected || attemptID != nil
                audioConnected = false; dialing = false; attemptID = nil
                if hadLocalCall { audio.end() }
                hostHangupPending = false
                hostHangupUnconfirmed = false
                hostHangupAttemptID = nil
            }
            if let sdp = next.sdp, sdp != appliedSDP, next.attemptId == attemptID {
                appliedSDP = sdp
                try await audio.answer(sdp)
            }
            if let id = next.session?.id, let timelineAPI {
                do {
                    let nextDetail = try await timelineAPI.sessionDetail(id: id, includeTimeline: true)
                    guard !closed, current == generation else { return }
                    detail = nextDetail; activityError = nil
                }
                catch { if !closed, current == generation { activityError = "Activity reconnecting: \(error.localizedDescription)" } }
            }
        } catch {
            guard !closed, current == generation else { return }
            connectionError = "Reconnecting: \(error.localizedDescription)"
            if audioConnected { hangup() }
        }
    }

    func call() async {
        guard let api, canCall else { return }
        working = true; dialing = true; error = nil; microphoneDenied = false; generation += 1
        let current = generation
        defer { working = false }
        do {
            let selected = try await api.action(VoiceAction(action: "select", targetId: targetID.trimmingCharacters(in: .whitespacesAndNewlines)))
            guard !closed, generation == current else { return }
            state = selected
            let next = try await api.action(VoiceAction(action: "prepare"))
            guard !closed, generation == current else { return }
            state = next
            attemptID = UUID().uuidString
            appliedSDP = nil; channelOpen = false; connectedReported = false; startSubmitted = false; muted = false
            try await audio.start()
        } catch {
            guard !closed, generation == current else { return }
            if !(error is CancellationError) { self.error = error.localizedDescription }
            microphoneDenied = error is VoiceMicrophoneDenied
            dialing = false; audio.end()
        }
    }

    private func receive(_ type: String, _ value: String) {
        guard !closed else { return }
        switch type {
        case "loading": ready = false
        case "ready": ready = true
        case "unavailable": ready = false; error = value; hangup()
        case "offer":
            guard let api, let id = attemptID else { return }
            let current = generation
            Task {
                startSubmitted = true
                do {
                    let next = try await api.action(VoiceAction(action: "start", attemptId: id, sdp: value))
                    if closed || generation != current {
                        _ = try? await api.action(VoiceAction(action: "stop", attemptId: id)); return
                    }
                    state = next
                } catch {
                    guard !closed, generation == current else { return }
                    self.error = error.localizedDescription
                    dialing = false
                    audio.end()
                    _ = try? await api.action(VoiceAction(action: "stop", attemptId: id))
                }
            }
        case "connection":
            audioConnected = value == "connected"
            if ["failed", "disconnected", "closed"].contains(value) { error = "Audio disconnected. Hang up, then call again to resume this operator."; hangup() }
            reportConnected()
        case "channel": channelOpen = value == "open"; reportConnected()
        case "error": error = value; hangup()
        default: break
        }
    }

    private func reportConnected() {
        guard audioConnected, channelOpen, !connectedReported, let api, let id = attemptID else { return }
        connectedReported = true
        dialing = false
        let current = generation
        Task {
            do {
                let next = try await api.action(VoiceAction(action: "connected", attemptId: id))
                if !closed, current == generation { state = next }
            }
            catch { if !closed, current == generation { self.error = error.localizedDescription; hangup() } }
        }
    }

    func toggleMute() { muted.toggle(); audio.mute(muted) }

    func hangup() {
        generation += 1
        let current = generation
        dialing = false; audioConnected = false; channelOpen = false
        audio.end()
        let localAttempt = attemptID
        let id = localAttempt ?? hostHangupAttemptID ?? state?.attemptId
        let shouldStopHost = startSubmitted || hostHangupUnconfirmed || (localAttempt == nil && state?.hasCall == true)
        attemptID = nil
        startSubmitted = false
        guard shouldStopHost, let api, let id, state?.occupied != true else { return }
        hostHangupPending = true
        hostHangupUnconfirmed = false
        hostHangupAttemptID = id
        Task {
            do {
                let next = try await api.action(VoiceAction(action: "stop", attemptId: id))
                if !closed, current == generation { state = next }
            }
            catch {
                if !closed, current == generation {
                    self.hostHangupPending = false
                    self.hostHangupUnconfirmed = true
                    if self.error == nil {
                        self.error = "Audio is off locally. Host hangup is unconfirmed: \(error.localizedDescription)"
                    }
                }
            }
        }
    }

    func sendText() async {
        guard let api, !closed, !working, let id = attemptID, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
        working = true; defer { working = false }
        let current = generation
        let messageID = UUID().uuidString
        let submitted = text; text = ""
        do {
            let next = try await api.action(VoiceAction(action: "text", attemptId: id, text: submitted, messageId: messageID))
            if !closed, generation == current { state = next }
        }
        catch { if !closed, generation == current { self.error = "Text outcome is unknown. Check the conversation before resending. \(error.localizedDescription)" } }
    }

    func transfer() async {
        guard let api, !closed, !working, state?.phase == "connected", state?.occupied != true else { return }
        working = true
        defer { working = false }
        let current = generation
        do {
            let next = try await api.action(VoiceAction(action: "transfer", targetId: targetID.trimmingCharacters(in: .whitespacesAndNewlines)))
            if !closed, generation == current { state = next }
        } catch {
            if !closed, generation == current { self.error = error.localizedDescription }
        }
    }

    func interrupt() async {
        guard let api, !closed else { return }
        let current = generation
        do {
            let next = try await api.action(VoiceAction(action: "interrupt"))
            if !closed, generation == current { state = next }
        }
        catch { if !closed, generation == current { self.error = error.localizedDescription } }
    }

    func interruptTarget() async {
        guard let api, !closed, !working, state?.phase == "connected", state?.occupied != true, state?.target != nil else { return }
        working = true
        defer { working = false }
        let current = generation
        do {
            let next = try await api.action(VoiceAction(action: "target-interrupt", attemptId: state?.attemptId))
            if !closed, generation == current { state = next }
        } catch {
            if !closed, generation == current { self.error = error.localizedDescription }
        }
    }

    func startNewConversation() async {
        guard let api, canStartNewConversation else { return }
        working = true; error = nil; generation += 1
        let current = generation
        defer { working = false }
        do {
            let next = try await api.action(VoiceAction(action: "new"))
            guard !closed, generation == current else { return }
            state = next
            detail = nil
            activityError = nil
        } catch {
            guard !closed, generation == current else { return }
            self.error = error.localizedDescription
        }
    }

    var connectionTitle: String {
        if hostHangupPending { return "Audio off locally; confirming host hangup…" }
        if hostHangupUnconfirmed { return "Audio off locally; host hangup unconfirmed" }
#if os(macOS)
        if connectionError != nil { return "Reconnecting to Agenthail" }
#else
        if connectionError != nil { return "Reconnecting to your Mac" }
#endif
        if audioConnected { return muted ? "Microphone muted" : "Connected" }
        if dialing { return "Connecting audio…" }
        switch state?.phase {
        case "starting", "negotiating": return "Connecting audio…"
        case "stopping": return "Ending call…"
        case "unknown": return "Connection needs attention"
        case "blocked": return "Voice unavailable"
        case "creating": return "Preparing orchestrator…"
        case "connected": return "Previous call needs hangup"
        case "ready", "idle", "ended": return "Ready to talk"
#if os(macOS)
        default: return "Connecting to Agenthail…"
#else
        default: return "Connecting to your Mac…"
#endif
        }
    }

    func close() {
        guard !closed else { return }
        hangup()
        closed = true
        state = nil
        detail = nil
        polling?.cancel(); polling = nil
        audio.close()
    }
}
