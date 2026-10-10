import AVFAudio
import OSLog
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
    @Published var audioConnected = false
    @Published var audioSuspended = false
    @Published var held = false
    @Published var mediaReconnecting = false
    @Published private(set) var hostHangupPending = false
    @Published private(set) var hostHangupUnconfirmed = false
    @Published var text = ""
    @Published var targetID = ""
    let isPreview: Bool
    let audio: any VoiceAudioClient
    let diagnostics: VoiceCallDiagnostics
    private var api: (any VoiceServiceClient)?
    private var timelineAPI: AgenthailAPI?
    private var polling: Task<Void, Never>?
    private var heartbeat: Task<Void, Never>?
    private var activityRefresh: Task<Void, Never>?
    private var mediaRecovery: Task<Void, Never>?
    private var contextDispatching = false
    private var contextSent: UInt64 = 0
    private var contextAttempt: String?
    private var contextBindingGeneration: UInt64 = 0
    private var visibleScreens = 0
    private var lastActivityRefresh = Date.distantPast
    private var callStartedAt: ContinuousClock.Instant?
    private let logger = Logger(subsystem: "systems.agenthail", category: "voice-call")
    private var generation = 0
    private var attemptID: String?
    private var appliedSDP: String?
    private var channelOpen = false
    private var inputRevision: UInt64 = 0
    private var connectedReported = false
    private var startSubmitted = false
    private var hostHangupAttemptID: String?
    private var closed = false
    var hasLocalCall: Bool { dialing || audioConnected || attemptID != nil }
    var committedRoute: String { state?.target?.name ?? "Agenthail orchestrator" }
    var callUnavailableReason: String? {
        if closed { return "closed" }
        if !ready { return "audio_not_ready" }
        if working { return "action_in_progress" }
        if dialing { return "dialing" }
        if audioConnected { return "audio_connected" }
        if hostHangupPending { return "hangup_pending" }
        if hostHangupUnconfirmed { return "hangup_unconfirmed" }
        if state?.hasCall == true { return "host_call_active" }
        if state?.phase == "blocked" { return "host_blocked" }
        if connectionError != nil { return "host_unavailable" }
        return nil
    }
    var canCall: Bool { callUnavailableReason == nil }
    var canStartNewConversation: Bool {
        state != nil && !working && !dialing && !audioConnected && !hostHangupPending && !hostHangupUnconfirmed
            && !closed && state?.hasCall != true && state?.occupied != true
            && state?.phase != "creating" && state?.phase != "blocked"
    }

    init(preview: Bool = false, targetID: String = "", api: (any VoiceServiceClient)? = nil, audio: (any VoiceAudioClient)? = nil) {
        isPreview = preview
        self.targetID = targetID
        self.audio = audio ?? VoiceAudioBridge()
        diagnostics = VoiceCallDiagnostics(file: api == nil && !preview ? FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first?.appendingPathComponent("voice-attempts.json") : nil)
#if DEBUG
        if preview {
            state = try? JSONDecoder().decode(VoiceState.self, from: Data(Self.previewState.utf8))
            detail = try? JSONDecoder().decode(SessionDetail.self, from: Data(SessionPreview.previewDetailJSON.utf8))
            ready = true
            return
        }
#endif
        if let api {
            self.api = api
            self.audio.onMessage = { [weak self] type, value in self?.receive(type, value) }
            return
        }
        do {
            guard let endpoint = KeychainStore.get("endpoint").flatMap(URL.init(string:)),
                  let token = KeychainStore.get("token") else { throw AgenthailAPIError.unavailable("Pair this phone with Agenthail on your Mac first.") }
            self.api = try VoiceAPI(endpoint: endpoint, token: token)
            timelineAPI = AgenthailAPI(baseURL: endpoint, token: token)
        } catch { self.error = error.localizedDescription }
        self.audio.onMessage = { [weak self] type, value in self?.receive(type, value) }
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

    func refreshPreview() {
        guard isPreview else { return }
        state = try? JSONDecoder().decode(VoiceState.self, from: Data(Self.previewState.utf8))
    }
#endif

    func open() {
        guard let api, polling == nil, !closed else { return }
        audio.load(api.request(path: "api/v1/voice"))
        polling = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.refresh()
                try? await Task.sleep(for: .seconds(self.audioConnected ? 3 : 1))
            }
        }
    }

    func refresh() async {
        guard let api, !closed else { return }
        let current = generation
        do {
            let next = try await api.state()
            guard !closed, current == generation else { return }
            if next.phase == "unknown", hasLocalCall, next.attemptId == attemptID {
                state = next
                error = error ?? next.message ?? "The host can no longer confirm this call. Audio has stopped."
                hangup(cause: "host_closed")
                return
            }
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
                if let id = attemptID ?? hostHangupAttemptID, id == next.attemptId {
                    diagnostics.mark(next.phase == "ended" ? "host_closed" : "host_blocked", id: id)
                    diagnostics.end(next.phase == "ended" ? "host_closed" : "host_blocked", id: id)
                }
                audioConnected = false; dialing = false; attemptID = nil
                audioSuspended = false; held = false; mediaReconnecting = false
                if hadLocalCall { audio.end() }
                heartbeat?.cancel(); heartbeat = nil
                mediaRecovery?.cancel(); mediaRecovery = nil
                hostHangupPending = false
                hostHangupUnconfirmed = false
                hostHangupAttemptID = nil
                if next.phase == "ended" || visibleScreens == 0 { polling?.cancel(); polling = nil }
            }
            if let sdp = next.sdp, sdp != appliedSDP, next.attemptId == attemptID {
                appliedSDP = sdp
                try await audio.answer(sdp)
                if current == generation, let attemptID { diagnostics.mark("sdp_applied", id: attemptID) }
            }
            if audioConnected, channelOpen, next.attemptId == attemptID {
                await dispatchContext(next, generation: current)
            }
            refreshActivity(sessionID: next.session?.id, generation: current)
        } catch {
            guard !closed, current == generation else { return }
            connectionError = "Reconnecting: \(error.localizedDescription)"
        }
    }

    private func dispatchContext(_ next: VoiceState, generation current: Int) async {
        guard !contextDispatching, let api, let id = attemptID, next.attemptId == id else { return }
        contextDispatching = true
        defer { contextDispatching = false }
        if contextAttempt != id { contextAttempt = id; contextSent = 0; contextBindingGeneration = 0 }
        contextBindingGeneration = max(contextBindingGeneration, max(next.bindingGeneration ?? 0, state?.bindingGeneration ?? 0))
        do {
            for frame in (next.contextFrames ?? []).sorted(by: { $0.id < $1.id }) where frame.id > contextSent {
                guard !closed, generation == current, attemptID == id else { return }
                contextBindingGeneration = max(contextBindingGeneration, state?.bindingGeneration ?? 0)
                guard frame.generation == contextBindingGeneration else { contextSent = frame.id; continue }
                try audio.appendContext(frame.text)
                contextSent = frame.id
                diagnostics.event("context_commentary_sent", id: id)
            }
            if contextSent > 0, !(next.contextFrames ?? []).isEmpty {
                _ = try await api.action(VoiceAction(action: "context-ack", attemptId: id, contextAck: contextSent))
            }
        } catch {
            guard !closed, generation == current, attemptID == id else { return }
            diagnostics.failure(error, stage: "context_dispatch", id: id)
            self.error = "Voice context could not reach the call. Audio stopped to avoid replaying an uncertain update."
            hangup(cause: "context_dispatch_failed")
        }
    }

    func screenAppeared() { visibleScreens += 1; open() }

    func screenDisappeared() {
        visibleScreens = max(0, visibleScreens - 1)
        if visibleScreens == 0 {
            activityRefresh?.cancel(); activityRefresh = nil
            if !hasLocalCall && !hostHangupPending && !hostHangupUnconfirmed {
                polling?.cancel(); polling = nil
            }
        }
    }

    private func refreshActivity(sessionID: String?, generation current: Int) {
        guard visibleScreens > 0, !dialing, activityRefresh == nil,
              Date().timeIntervalSince(lastActivityRefresh) >= 5,
              let id = sessionID, let timelineAPI else { return }
        lastActivityRefresh = Date()
        activityRefresh = Task { [weak self] in
            guard let self else { return }
            defer { self.activityRefresh = nil }
            do {
                let next = try await timelineAPI.sessionDetail(id: id, includeTimeline: true)
                guard !Task.isCancelled, !self.closed, current == self.generation else { return }
                self.detail = next; self.activityError = nil
            } catch {
                guard !Task.isCancelled, !self.closed, current == self.generation else { return }
                self.activityError = "Activity reconnecting: \(error.localizedDescription)"
            }
        }
    }

    func activityAttachment(_ item: TimelineItem) async -> Data? {
        guard let timelineAPI, let id = detail?.session.id, let attachment = item.attachment else { return nil }
        let data = try? await timelineAPI.sessionAttachment(sessionID: id, id: attachment.id)
        guard !Task.isCancelled, !closed, detail?.session.id == id else { return nil }
        return data
    }

    private func startHeartbeat(attempt: String, generation current: Int) {
        heartbeat?.cancel()
        heartbeat = Task { [weak self] in
            var lastSuccess = ContinuousClock.now
            while !Task.isCancelled {
                guard let self, !self.closed, current == self.generation,
                      self.attemptID == attempt, let api = self.api else { return }
                do {
                    let next = try await api.action(VoiceAction(action: "heartbeat", attemptId: attempt))
                    guard !Task.isCancelled, current == self.generation else { return }
                    lastSuccess = .now
                    if ["ended", "blocked", "unknown"].contains(next.phase) {
                        self.error = self.error ?? next.message ?? "The host ended this call."
                        self.hangup(cause: "host_closed")
                        return
                    }
                } catch {
                    guard !Task.isCancelled, current == self.generation else { return }
                    if lastSuccess.duration(to: .now) >= .seconds(30) {
                        self.error = self.error ?? "The Mac connection was lost. Audio has stopped; call again when it reconnects."
                        self.hangup(cause: "heartbeat_expired")
                        return
                    }
                }
                do { try await Task.sleep(for: .seconds(10)) } catch { return }
            }
        }
    }

    func call() async {
        guard let api, canCall else { return }
        working = true; dialing = true; error = nil; generation += 1
        callStartedAt = .now
        let current = generation
        let callID = UUID()
        attemptID = callID.uuidString
        diagnostics.begin(id: callID.uuidString)
        appliedSDP = nil; channelOpen = false; connectedReported = false; startSubmitted = false; muted = false
        audioSuspended = false; held = false; mediaReconnecting = false
        defer { if generation == current { working = false } }
        open()
        var stage = "target_select"
        do {
            let selected = try await api.action(VoiceAction(action: "select", targetId: targetID.trimmingCharacters(in: .whitespacesAndNewlines)))
            guard !closed, generation == current else { return }
            state = selected
            stage = "operator_prepare"
            let next = try await api.action(VoiceAction(action: "prepare"))
            guard !closed, generation == current else { return }
            state = next
            stage = "native_audio_start"
            audio.configureCall(id: callID, name: committedRoute)
            try await audio.start()
        } catch {
            guard !closed, generation == current else { return }
            if !(error is CancellationError) { self.error = error.localizedDescription }
            if let attemptID {
                diagnostics.failure(error, stage: stage, id: attemptID)
                diagnostics.end("\(stage)_failed", id: attemptID)
            }
            dialing = false; attemptID = nil; audio.end()
        }
    }

    private func receive(_ type: String, _ value: String) {
        guard !closed else { return }
        switch type {
        case "permission", "peer", "activation":
            if let attemptID {
                diagnostics.mark(type == "permission" ? "microphone_\(value)" : type, id: attemptID)
                if type == "permission", value == "denied" { diagnostics.end("microphone_denied", id: attemptID) }
            }
        case "loading": ready = false
        case "ready": ready = true
        case "unavailable": ready = false; error = value; hangup()
        case "offer":
            guard let api, let id = attemptID else { return }
            let current = generation
            startSubmitted = true
            diagnostics.mark("offer_submitted", id: id)
            Task {
                guard !closed, generation == current else { return }
                do {
                    let next = try await api.action(VoiceAction(action: "start", attemptId: id, sdp: value, heartbeatMode: "explicit", contextTransport: "native-commentary"))
                    if closed || generation != current {
                        _ = try? await api.action(VoiceAction(action: "stop", attemptId: id)); return
                    }
                    state = next
                    diagnostics.mark("host_start_accepted", id: id)
                    audio.mute(muted)
                    startHeartbeat(attempt: id, generation: current)
                } catch {
                    guard !closed, generation == current else { return }
                    self.error = self.error ?? error.localizedDescription
                    diagnostics.failure(error, stage: "host_start", id: id)
                    hangup(cause: "host_start_rejected")
                }
            }
        case "connection":
            audioConnected = value == "connected"
            if value == "connected", let attemptID { diagnostics.mark("peer_connected", id: attemptID) }
            mediaReconnecting = value == "disconnected"
            if value == "connected" { mediaRecovery?.cancel(); mediaRecovery = nil }
            if value == "disconnected", mediaRecovery == nil {
                let current = generation
                mediaRecovery = Task { [weak self] in
                    do { try await Task.sleep(for: .seconds(10)) } catch { return }
                    guard let self, current == self.generation, !self.audioConnected else { return }
                    self.error = self.error ?? "Audio could not reconnect. Call again to resume this session."
                    self.hangup(cause: "peer_failed")
                }
            }
            if ["failed", "closed"].contains(value) { error = error ?? "Audio disconnected. Call again to resume this session."; hangup(cause: "peer_failed") }
            reportConnected()
        case "channel":
            channelOpen = value == "open"
            if channelOpen, let attemptID { diagnostics.mark("data_channel_open", id: attemptID) }
            reportConnected()
        case "mute": muted = value == "true"
        case "input-muted":
            guard let api, let id = attemptID, startSubmitted else { return }
            inputRevision += 1
            let revision = inputRevision
            let current = generation
            diagnostics.event("input_muted_\(value)", id: id)
            Task {
                do {
                    let next = try await api.action(VoiceAction(action: "input-state", attemptId: id, muted: value == "true", inputRevision: revision))
                    guard !closed, current == generation else { return }
                    if (next.inputRevision ?? 0) >= revision { state = next }
                } catch {
                    guard !closed, current == generation else { return }
                    diagnostics.failure(error, stage: "input_state", id: id)
                    self.error = "Local microphone state could not be confirmed by the host. End the call before issuing more voice instructions."
                    hangup(cause: "input_state_failed")
                }
            }
        case "realtime-event":
            if let attemptID { diagnostics.event("\(value)_\(muted || held ? "muted" : "unmuted")", id: attemptID) }
        case "suspended": audioSuspended = value == "true"
        case "held": held = value == "true"
        case "ended": hangup(cause: value == "system_reset" ? "system_reset" : "user_hangup")
        case "error": error = error ?? value; hangup(cause: "data_channel_failed")
        default: break
        }
    }

    private func reportConnected() {
        guard audioConnected, channelOpen, !connectedReported, let api, let id = attemptID else { return }
        connectedReported = true
        dialing = false
        if let callStartedAt {
            let elapsed = callStartedAt.duration(to: .now)
            logger.info("Native voice media connected after \(String(describing: elapsed), privacy: .public)")
        }
        let current = generation
        Task {
            do {
                let next = try await api.action(VoiceAction(action: "connected", attemptId: id))
                if !closed, current == generation {
                    state = next
                    diagnostics.mark("host_connected", id: id)
                }
            }
            catch { if !closed, current == generation { self.error = error.localizedDescription; hangup(cause: "host_start_rejected") } }
        }
    }

    func toggleMute() { muted.toggle(); audio.mute(muted) }

    func hangup(cause: String = "user_hangup") {
        if let attemptID { diagnostics.end(cause, id: attemptID) }
        generation += 1
        working = false
        activityRefresh?.cancel(); activityRefresh = nil
        heartbeat?.cancel(); heartbeat = nil
        mediaRecovery?.cancel(); mediaRecovery = nil
        let current = generation
        dialing = false; audioConnected = false; channelOpen = false
        audioSuspended = false; held = false; mediaReconnecting = false
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
                diagnostics.mark("host_stop_accepted", id: id)
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

    func recordCallAvailability() {
        let reason = callUnavailableReason ?? "available"
        logger.info("Voice call button state: \(reason, privacy: .public)")
        if let id = diagnostics.attempts.last?.id {
            diagnostics.mark("call_button_\(reason)", id: id)
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

    func close() {
        guard !closed else { return }
        hangup()
        closed = true
        state = nil
        detail = nil
        polling?.cancel(); polling = nil
        activityRefresh?.cancel(); activityRefresh = nil
        audio.close()
    }
}

private struct VoiceScrollSize: Equatable {
    let viewport: CGFloat
    let content: CGFloat
}

struct AgenthailVoiceOperatorSheet: View {
    @ObservedObject private var model: VoiceOperatorModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var confirmInterrupt = false
    @State private var confirmTargetInterrupt = false
    @State private var confirmNewConversation = false
    @State private var showKeyboard = false
    @State private var showDetails = false
    @State private var showTransfer = false
    @State private var showActivity = false
    @State private var followingLatest = true
    @State private var atLatest = true
    @State private var userScrolling = false
    @State private var scrollPosition = ScrollPosition(edge: .bottom)
    let openSession: (String) -> Void

    init(model: VoiceOperatorModel, openSession: @escaping (String) -> Void) {
        self.model = model
        self.openSession = openSession
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                    if model.isPreview { Text("Sample conversation").font(.caption).foregroundStyle(.secondary) }
                    HStack(spacing: 12) {
                        Image(systemName: model.audioConnected ? "waveform" : "waveform.slash")
                            .font(.title2).foregroundStyle(model.audioConnected ? Color.accentColor : Color.secondary)
                            .frame(width: 44, height: 44).background(.quaternary, in: Circle())
                        VStack(alignment: .leading, spacing: 4) {
                            Text(connectionTitle).font(.headline)
                            Text(model.committedRoute).font(.subheadline).foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 0)
                        if model.working || model.dialing { ProgressView() }
                    }.padding(.vertical, 12)
                    if let error = model.error { Text(error).foregroundStyle(.red).textSelection(.enabled) }
                    if let connectionError = model.connectionError { Text(connectionError).foregroundStyle(.orange) }
                    if let message = model.state?.message, !message.isEmpty { Text(message).foregroundStyle(.orange).textSelection(.enabled) }
                    if model.state?.occupied == true { Text("Another paired device owns this call. You can inspect the operator timeline.") }
                    if let state = model.state, !state.transcripts.isEmpty {
                        ForEach(state.transcripts) { item in
                            IOSMessage(label: item.label, text: item.text, color: .secondary)
                        }
                    } else {
                        VStack(alignment: .leading, spacing: 20) {
                            Text("What would you like to work on?").font(.title2.weight(.semibold))
                            Text("Talk to the Agenthail orchestrator, or route this call to one exact existing Codex or Claude session.").font(.body).foregroundStyle(.secondary)
                            VStack(alignment: .leading, spacing: 14) {
                                Label("What are you working on?", systemImage: "bubble.left")
                                Label("Review the failing tests.", systemImage: "bubble.left")
                            }.font(.subheadline).foregroundStyle(.secondary)
                        }.padding(.vertical, 16)
                    }
                    if model.state?.truncated == true { Text("Showing recent voice activity. Recorded agent work remains in the full timeline.").font(.caption).foregroundStyle(.secondary) }
                    if let activityError = model.activityError { Text(activityError).font(.caption).foregroundStyle(.orange) }
                    if let detail = model.detail {
                        DisclosureGroup(isExpanded: $showActivity) {
                            VStack(alignment: .leading, spacing: 24) {
                                if detail.readOnly { Text(detail.readOnlyReason).foregroundStyle(.orange) }
                                if let timeline = detail.timeline {
                                    if let unavailable = timeline.unavailableReason { Text(unavailable).foregroundStyle(.secondary) }
                                    ForEach(TimelineGroup.make(timeline.items)) { group in
                                        CompactActivityGroup(group: group, attachmentLoader: { item in await model.activityAttachment(item) }) { followingLatest = false }
                                    }
                                    if timeline.truncated { Text("Earlier activity is in the full timeline.").font(.caption) }
                                }
                                Button("Open full timeline") { openSession(detail.session.id) }
                            }.padding(.top, 12)
                        } label: {
                            Label(detail.session.status == "busy" ? "Orchestrator is working" : "Agent activity", systemImage: "terminal")
                                .font(.subheadline.weight(.medium)).frame(minHeight: 44)
                                .accessibilityIdentifier("voice-activity")
                        }
                    }
                    Color.clear.frame(height: 1).id("voice-bottom")
                }.frame(maxWidth: 640).padding(.horizontal, 24).padding(.bottom, 24).frame(maxWidth: .infinity)
                }
                .defaultScrollAnchor(.top, for: .alignment)
                .defaultScrollAnchor(.bottom, for: .initialOffset)
                .scrollPosition($scrollPosition)
                .onScrollGeometryChange(for: VoiceScrollSize.self) { geometry in
                    VoiceScrollSize(viewport: geometry.containerSize.height, content: geometry.contentSize.height)
                } action: { old, next in
                    if followingLatest, !showActivity, old.content != next.content {
                        scrollPosition.scrollTo(edge: .bottom)
                    }
                }
                .onScrollPhaseChange { _, phase in
                    if phase == .interacting {
                        userScrolling = true
                        followingLatest = false
                    } else if phase == .idle && userScrolling {
                        followingLatest = atLatest
                        userScrolling = false
                    }
                }
                .onScrollGeometryChange(for: Bool.self) { geometry in
                    geometry.contentSize.height - geometry.visibleRect.maxY < 80
                } action: { _, value in
                    atLatest = value
                }
                .onChange(of: latestContentMarker) { _, _ in
                    if followingLatest, !showActivity { scrollPosition.scrollTo(edge: .bottom) }
                }
                .onChange(of: showActivity) { _, expanded in
                    if expanded { followingLatest = false }
                }
                .onChange(of: model.state?.session?.id) { _, _ in
                    followingLatest = true
                    atLatest = true
                    scrollPosition.scrollTo(edge: .bottom)
                }
                VStack(spacing: 0) {
                    Button {
                        followingLatest = true
                        scrollPosition.scrollTo(edge: .bottom)
                    } label: {
                        Image(systemName: "arrow.down")
                            .font(.body.weight(.semibold))
                            .frame(width: 44, height: 44)
                            .background(.regularMaterial, in: Circle())
                    }
                    .buttonStyle(.plain)
                    .opacity(atLatest ? 0 : 1)
                    .allowsHitTesting(!atLatest)
                    .accessibilityHidden(atLatest)
                    .accessibilityLabel("Jump to latest voice activity")
                    controls
                }
            }
            .navigationTitle(model.state?.target == nil ? "Talk to orchestrator" : "Session voice")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) { Button("Done") { dismiss() } }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button("New conversation", systemImage: "square.and.pencil") { confirmNewConversation = true }
                        .disabled(!model.canStartNewConversation)
                        .accessibilityIdentifier("new-voice-conversation")
                    Button("Connection details", systemImage: "info.circle") { showDetails = true }
                }
            }
            .sheet(isPresented: $showDetails) { connectionDetails }
            .onAppear { model.screenAppeared() }
#if DEBUG
            .task {
                guard model.isPreview, ProcessInfo.processInfo.arguments.contains("--preview-voice-refresh") else { return }
                for _ in 0..<40 {
                    do { try await Task.sleep(for: .milliseconds(150)) } catch { return }
                    model.refreshPreview()
                }
            }
#endif
            .onChange(of: model.callUnavailableReason) { _, _ in model.recordCallAvailability() }
            .onDisappear { model.screenDisappeared() }
            .confirmationDialog("Interrupt the orchestrator's current turn? This does not stop delegated agents.", isPresented: $confirmInterrupt) {
                Button("Interrupt orchestrator", role: .destructive) { Task { await model.interrupt() } }
            }
            .confirmationDialog("Stop the selected session's confirmed active turn? Audio stays connected.", isPresented: $confirmTargetInterrupt) {
                Button("Stop selected session", role: .destructive) { Task { await model.interruptTarget() } }
            } message: {
                Text("This does not hang up the call or stop another agent.")
            }
            .confirmationDialog("Start a new orchestrator conversation?", isPresented: $confirmNewConversation, titleVisibility: .visible) {
                Button("Start new conversation") {
                    followingLatest = true
                    Task { await model.startNewConversation() }
                }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("This creates a separate Codex thread. The current conversation remains available in Sessions.")
            }
        }
    }

    private var latestContentMarker: String {
        let transcript = model.state?.transcripts.last
        let item = model.detail?.timeline?.items.last
        return "\(transcript?.id ?? ""):\(transcript?.text ?? ""):\(item?.id ?? ""):\(item?.text ?? "")"
    }

    private var controls: some View {
        VStack(spacing: 16) {
            if showKeyboard && model.audioConnected {
                HStack(alignment: .bottom, spacing: 12) {
                    TextField("Message selected session", text: $model.text, axis: .vertical)
                        .lineLimit(1...5).padding(12).background(.quaternary, in: RoundedRectangle(cornerRadius: 12))
                    Button("Send", systemImage: "arrow.up") { Task { await model.sendText() } }
                        .labelStyle(.iconOnly).frame(width: 44, height: 44).background(.tint, in: Circle()).foregroundStyle(.white)
                        .disabled(model.working || model.text.isEmpty)
                }
            }
            if model.hostHangupPending {
                callEntry
            } else if model.hostHangupUnconfirmed {
                callControl("Try hangup again", icon: "phone.down.fill", destructive: true, disabled: model.state?.occupied == true) { model.hangup() }
                if !dynamicTypeSize.isAccessibilitySize { Text("Audio is off locally. The host did not confirm hangup.").font(.caption).foregroundStyle(.secondary) }
            } else if model.state?.hasCall == true || model.audioConnected || model.dialing {
                if model.audioConnected && model.state?.occupied != true {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Connected to \(model.committedRoute)").font(.subheadline)
                        Text("Ask the orchestrator to transfer you to another agent.")
                            .font(.caption).foregroundStyle(.secondary)
                        HStack {
                            Button("Transfer…") { showTransfer.toggle() }.buttonStyle(.bordered)
                            if model.state?.target != nil {
                                Button("Return to orchestrator") {
                                    model.targetID = ""
                                    Task { await model.transfer() }
                                }
                                    .buttonStyle(.borderless)
                                    .disabled(model.working)
                                Button("Stop selected session", role: .destructive) { confirmTargetInterrupt = true }
                                    .buttonStyle(.borderless)
                                    .disabled(model.working)
                            }
                        }
                        if showTransfer {
                            HStack {
                                TextField("Agent alias or session", text: $model.targetID)
                                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                                Button("Transfer") { Task { await model.transfer(); showTransfer = false } }
                                    .disabled(model.working || model.targetID.isEmpty)
                            }
                        }
                    }
                }
                HStack(alignment: .top, spacing: 32) {
                    callControl("Type", icon: "keyboard", disabled: !model.audioConnected) { showKeyboard.toggle() }
                    callControl(model.muted ? "Unmute" : "Mute", icon: model.muted ? "mic.slash.fill" : "mic.fill", disabled: !model.audioConnected, action: model.toggleMute)
                    callControl("Hang up", icon: "phone.down.fill", destructive: true, disabled: model.state?.occupied == true) { model.hangup() }
                }
                if !dynamicTypeSize.isAccessibilitySize { Text("Audio ends. Your agents keep working.").font(.caption).foregroundStyle(.secondary) }
            } else {
                callEntry
            }
        }.frame(maxWidth: 640).padding(.horizontal, 24).padding(.vertical, 16).frame(maxWidth: .infinity).background(.bar)
    }

    private var callEntry: some View {
        VStack(spacing: 16) {
            Button { Task { await model.call() } } label: {
                Label(dynamicTypeSize.isAccessibilitySize ? "Call" : (model.targetID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Talk to orchestrator" : "Call session"), systemImage: "phone.fill")
                    .font(.headline).frame(maxWidth: .infinity, minHeight: 48)
            }
            .buttonStyle(VoiceCallButtonStyle())
            .accessibilityLabel("Start voice call")
            .disabled(!model.canCall)
            if !dynamicTypeSize.isAccessibilitySize {
                Text(model.hostHangupPending ? "Audio ended locally. Confirming host hangup…" : (model.targetID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "The orchestrator can inspect, plan, and coordinate across Agenthail." : "Your conversation and agent work stay in the selected session."))
                    .font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    .frame(minHeight: 32)
            }
        }
    }

    private func callControl(_ title: String, icon: String, destructive: Bool = false, disabled: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: 8) {
                Image(systemName: icon).font(.title3).frame(width: 52, height: 52)
                    .background(destructive ? Color.red : Color.secondary.opacity(0.12), in: Circle())
                    .foregroundStyle(destructive ? Color.white : Color.primary)
                Text(title).font(.caption).foregroundStyle(.primary)
            }
        }.disabled(disabled).accessibilityLabel(title)
    }

    private var connectionTitle: String {
        if model.hostHangupPending { return "Audio off locally; confirming host hangup…" }
        if model.hostHangupUnconfirmed { return "Audio off locally; host hangup unconfirmed" }
        if model.connectionError != nil { return "Reconnecting to your Mac" }
        if model.held { return "Call on hold" }
        if model.audioSuspended { return "Audio paused by iOS" }
        if model.mediaReconnecting { return "Reconnecting audio…" }
        if model.audioConnected { return model.muted ? "Microphone muted" : "Connected" }
        if model.dialing { return "Connecting audio…" }
        switch model.state?.phase {
        case "starting", "negotiating": return "Connecting audio…"
        case "stopping": return "Ending call…"
        case "unknown": return "Connection needs attention"
        case "blocked": return "Voice unavailable"
        case "creating": return "Preparing orchestrator…"
        case "connected": return "Previous call needs hangup"
        case "ready", "idle", "ended": return "Ready to talk"
        default: return "Connecting to your Mac…"
        }
    }

    private var connectionDetails: some View {
        NavigationStack {
            List {
                Section("Connection") {
                    LabeledContent("Provider", value: "Codex Voice")
                    if let licenseURL = Bundle.main.url(forResource: "WebRTC-LICENSE", withExtension: "txt"),
                       let license = try? String(contentsOf: licenseURL, encoding: .utf8) {
                        DisclosureGroup("WebRTC license") { Text(license).font(.caption).textSelection(.enabled) }
                    }
                    if let target = model.state?.target {
                        LabeledContent("Coding target", value: target.name)
                        LabeledContent("Coding provider", value: model.state?.codingProvider ?? target.surface)
                    }
                    if let provider = model.state?.audioProvider { LabeledContent("Audio provider", value: provider) }
                    LabeledContent("Audio", value: connectionTitle)
                    Text("The microphone sends audio to Codex. No on-device speech recognition is used.")
                    Text("The call stays active when you close this screen or lock your phone. Use Hang up or the system call controls to end audio. Force-quitting the app ends the call.")
                    if let state = model.state, let id = state.attemptId {
                        LabeledContent("Call ID") { Text(id).font(.caption.monospaced()).textSelection(.enabled) }
                    }
                    if model.state?.phase == "ended", let closed = model.state?.events.last(where: { $0.method == "thread/realtime/closed" }) {
                        LabeledContent("Host close") {
                            Text("\(closed.params.reason ?? "unspecified") · event #\(closed.sequence)")
                                .font(.caption.monospaced()).textSelection(.enabled)
                        }
                    }
                }
                if let session = model.state?.session {
                    Section("Persistent orchestrator") {
                        Text(session.name)
                        Text(session.id).font(.caption.monospaced()).textSelection(.enabled)
                        LabeledContent("Skill", value: "Agenthail operations")
                        Text(model.state?.skillDigest ?? "").font(.caption.monospaced()).textSelection(.enabled)
                        Button("Open full timeline") { showDetails = false; openSession(session.id) }
                    }
                }
                if let attempt = model.diagnostics.attempts.last {
                    Section("Call lifecycle") {
                        LabeledContent("Attempt") { Text(attempt.id).font(.caption.monospaced()).textSelection(.enabled) }
                        if let cause = attempt.terminalCause { LabeledContent("Ended because", value: cause) }
                        if let stage = attempt.failureStage {
                            LabeledContent("Failure stage", value: stage)
                            LabeledContent("Error code", value: "\(attempt.failureDomain ?? "application") · \(attempt.failureCode ?? 0)")
                        }
                        ForEach(attempt.milestones.keys.sorted(), id: \.self) { milestone in
                            LabeledContent(milestone, value: String(format: "%.3f s", attempt.milestones[milestone] ?? 0))
                        }
                        if let failure = model.diagnostics.persistenceError {
                            Text("Call diagnostics could not be saved: \(failure)").foregroundStyle(.red)
                        }
                    }
                }
                if model.detail?.session.status == "busy" {
                    Section {
                        Button("Interrupt orchestrator turn", role: .destructive) { showDetails = false; confirmInterrupt = true }
                            .disabled(model.detail?.readOnly == true || model.state?.occupied == true)
                        Text("This interrupts the orchestrator only, not the agents it has already messaged.").font(.caption)
                    }
                }
            }.navigationTitle("Voice details").navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .topBarTrailing) { Button("Done") { showDetails = false } } }
        }
    }
}

private struct VoiceCallButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(.white)
            .background(isEnabled ? SessionStyle.accent : Color.secondary, in: Capsule())
            .opacity(isEnabled ? (configuration.isPressed ? 0.85 : 1) : 0.45)
    }
}

struct VoiceOperatorEntry: View {
    @ObservedObject var model: AgenthailIOSModel
    @EnvironmentObject private var voiceCalls: VoiceCallCoordinator
    @State private var presented = false
    @State private var operatorID: String?

    var body: some View {
        Button(voiceCalls.model?.hasLocalCall == true ? "Return to call" : "Talk to orchestrator", systemImage: "waveform") {
            voiceCalls.open()
            presented = true
        }
            .font(.subheadline.weight(.semibold))
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(minHeight: 56)
            .accessibilityIdentifier("voice-entry")
            .sheet(isPresented: $presented) {
                if let call = voiceCalls.model {
                AgenthailVoiceOperatorSheet(model: call) { id in operatorID = id }
                    .sheet(isPresented: Binding(get: { operatorID != nil }, set: { if !$0 { operatorID = nil } })) {
                        if let id = operatorID {
                            NavigationStack {
                                SessionRouteView(model: model, sessionID: id)
                                    .toolbar { ToolbarItem(placement: .topBarTrailing) { Button("Back to Voice") { operatorID = nil } } }
                            }
                        }
                    }
                }
            }
            .onChange(of: model.isPaired) { _, paired in if !paired { presented = false } }
    }
}
