import AVFAudio
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
    @Published private(set) var hostHangupPending = false
    @Published private(set) var hostHangupUnconfirmed = false
    @Published var text = ""
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

    init(preview: Bool = false, api: (any VoiceServiceClient)? = nil, audio: (any VoiceAudioClient)? = nil) {
        isPreview = preview
        self.audio = audio ?? VoiceAudioBridge()
#if DEBUG
        if preview {
            state = try? JSONDecoder().decode(VoiceState.self, from: Data(Self.previewState.utf8))
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
        working = true; dialing = true; error = nil; generation += 1
        let current = generation
        defer { working = false }
        do {
            let next = try await api.action(VoiceAction(action: "prepare"))
            guard !closed, generation == current else { return }
            state = next
            attemptID = UUID().uuidString
            appliedSDP = nil; channelOpen = false; connectedReported = false; startSubmitted = false; muted = false
            try await audio.start()
        } catch {
            guard !closed, generation == current else { return }
            if !(error is CancellationError) { self.error = error.localizedDescription }
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

    func audioInterrupted(_ notification: Notification) {
        guard let rawValue = notification.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
              AVAudioSession.InterruptionType(rawValue: rawValue) == .began,
              dialing || audioConnected else { return }
        let reason = (notification.userInfo?[AVAudioSessionInterruptionReasonKey] as? UInt)
            .map { " (iOS reason code \($0))" } ?? ""
        error = "iOS interrupted the microphone\(reason). The call was ended; call again after the interruption clears."
        hangup()
    }

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

    func interrupt() async {
        guard let api, !closed else { return }
        let current = generation
        do {
            let next = try await api.action(VoiceAction(action: "interrupt"))
            if !closed, generation == current { state = next }
        }
        catch { if !closed, generation == current { self.error = error.localizedDescription } }
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
        polling?.cancel(); polling = nil
        audio.close()
    }
}

struct AgenthailVoiceOperatorSheet: View {
    @StateObject private var model: VoiceOperatorModel
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var confirmInterrupt = false
    @State private var confirmNewConversation = false
    @State private var showKeyboard = false
    @State private var showDetails = false
    @State private var followingLatest = true
    @State private var atLatest = true
    @State private var userScrolling = false
    let openSession: (String) -> Void

    init(model: VoiceOperatorModel? = nil, openSession: @escaping (String) -> Void) {
        _model = StateObject(wrappedValue: model ?? VoiceOperatorModel())
        self.openSession = openSession
    }

    var body: some View {
        NavigationStack {
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 24) {
                    if model.isPreview { Text("Sample conversation").font(.caption).foregroundStyle(.secondary) }
#if DEBUG && targetEnvironment(simulator)
                    if VoiceEvaluation.enabled {
                        HStack {
                            Text("Simulated microphone · Real Codex").font(.caption).foregroundStyle(.secondary)
                            Spacer()
                            Button("Speak next request") {
                                (model.audio as? VoiceAudioBridge)?.webView.evaluateJavaScript("void window.agenthailEvaluationSpeak()", completionHandler: nil)
                            }.font(.caption).disabled(!model.audioConnected)
                        }
                    }
#endif
                    HStack(spacing: 12) {
                        Image(systemName: model.audioConnected ? "waveform" : "waveform.slash")
                            .font(.title2).foregroundStyle(model.audioConnected ? Color.accentColor : Color.secondary)
                            .frame(width: 44, height: 44).background(.quaternary, in: Circle())
                        VStack(alignment: .leading, spacing: 4) {
                            Text(connectionTitle).font(.headline)
                            Text("Codex Voice").font(.subheadline).foregroundStyle(.secondary)
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
                            VStack(alignment: .leading, spacing: 8) {
                                Text(item.role == "user" ? "You" : "Orchestrator").font(.subheadline.weight(.medium)).foregroundStyle(.secondary)
                                Text(item.text).font(.body).lineSpacing(4).textSelection(.enabled)
                            }
                        }
                    } else {
                        VStack(alignment: .leading, spacing: 20) {
                            Text("What would you like to work on?").font(.title2.weight(.semibold))
                            Text("Talk through a plan, check on your agents, or ask the orchestrator to send them work.").font(.body).foregroundStyle(.secondary)
                            VStack(alignment: .leading, spacing: 14) {
                                Label("What are my agents working on?", systemImage: "bubble.left")
                                Label("Ask the builder to review the failing tests.", systemImage: "bubble.left")
                            }.font(.subheadline).foregroundStyle(.secondary)
                        }.padding(.vertical, 16)
                    }
                    if model.state?.truncated == true { Text("Showing recent voice activity. Recorded agent work remains in the full timeline.").font(.caption).foregroundStyle(.secondary) }
                    if let activityError = model.activityError { Text(activityError).font(.caption).foregroundStyle(.orange) }
                    if let detail = model.detail {
                        DisclosureGroup {
                            VStack(alignment: .leading, spacing: 12) {
                                if detail.readOnly { Text(detail.readOnlyReason).foregroundStyle(.orange) }
                                if let timeline = detail.timeline {
                                    if let unavailable = timeline.unavailableReason { Text(unavailable).foregroundStyle(.secondary) }
                                    ForEach(TimelineGroup.newestFirst(timeline.items)) { CompactActivityGroup(group: $0) }
                                    if timeline.truncated { Text("Earlier activity is in the full timeline.").font(.caption) }
                                }
                                Button("Open full timeline") { openSession(detail.session.id) }
                            }.padding(.top, 12)
                        } label: {
                            Label(detail.session.status == "busy" ? "Orchestrator is working" : "Agent activity", systemImage: "terminal")
                                .font(.subheadline.weight(.medium)).frame(minHeight: 44)
                        }
                    }
                    Color.clear.frame(height: 1).id("voice-bottom")
                }.frame(maxWidth: 640).padding(.horizontal, 24).padding(.bottom, 24).frame(maxWidth: .infinity)
                }
                .defaultScrollAnchor(.bottom, for: .initialOffset)
                .defaultScrollAnchor(followingLatest ? .bottom : nil, for: .sizeChanges)
                .defaultScrollAnchor(.top, for: .alignment)
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
                    if followingLatest { proxy.scrollTo("voice-bottom", anchor: .bottom) }
                }
                .onChange(of: model.state?.session?.id) { _, _ in
                    followingLatest = true
                    atLatest = true
                    proxy.scrollTo("voice-bottom", anchor: .bottom)
                }
                .safeAreaInset(edge: .bottom) {
                    VStack(spacing: 0) {
                        if !atLatest {
                            Button {
                                followingLatest = true
                                proxy.scrollTo("voice-bottom", anchor: .bottom)
                            } label: {
                                Image(systemName: "arrow.down")
                                    .font(.body.weight(.semibold))
                                    .frame(width: 44, height: 44)
                                    .background(.regularMaterial, in: Circle())
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("Jump to latest voice activity")
                        }
                        controls
                    }
                }
            }
            .background(alignment: .bottom) {
                if let bridge = model.audio as? VoiceAudioBridge { VoiceAudioSurface(bridge: bridge).frame(width: 1, height: 1).accessibilityHidden(true) }
            }
            .navigationTitle("Orchestrator")
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
            .task { model.open() }
            .onDisappear { model.close() }
            .onChange(of: scenePhase) { _, phase in if phase == .background { model.hangup() } }
            .onReceive(NotificationCenter.default.publisher(for: AVAudioSession.interruptionNotification)) { model.audioInterrupted($0) }
            .confirmationDialog("Interrupt the orchestrator's current turn? This does not stop delegated agents.", isPresented: $confirmInterrupt) {
                Button("Interrupt orchestrator", role: .destructive) { Task { await model.interrupt() } }
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
        let event = model.state?.events.last?.sequence ?? 0
        let item = model.detail?.timeline?.items.last?.id ?? ""
        return "\(event):\(item)"
    }

    private var controls: some View {
        VStack(spacing: 16) {
            if showKeyboard && model.audioConnected {
                HStack(alignment: .bottom, spacing: 12) {
                    TextField("Message the orchestrator", text: $model.text, axis: .vertical)
                        .lineLimit(1...5).padding(12).background(.quaternary, in: RoundedRectangle(cornerRadius: 12))
                    Button("Send", systemImage: "arrow.up") { Task { await model.sendText() } }
                        .labelStyle(.iconOnly).frame(width: 44, height: 44).background(.tint, in: Circle()).foregroundStyle(.white)
                        .disabled(model.working || model.text.isEmpty)
                }
            }
            if model.hostHangupPending {
                Label("Audio ended locally. Confirming host hangup…", systemImage: "hourglass")
                    .font(.subheadline).foregroundStyle(.secondary)
            } else if model.hostHangupUnconfirmed {
                callControl("Try hangup again", icon: "phone.down.fill", destructive: true, disabled: model.state?.occupied == true, action: model.hangup)
                if !dynamicTypeSize.isAccessibilitySize { Text("Audio is off locally. The host did not confirm hangup.").font(.caption).foregroundStyle(.secondary) }
            } else if model.state?.hasCall == true || model.audioConnected || model.dialing {
                HStack(alignment: .top, spacing: 32) {
                    callControl("Type", icon: "keyboard", disabled: !model.audioConnected) { showKeyboard.toggle() }
                    callControl(model.muted ? "Unmute" : "Mute", icon: model.muted ? "mic.slash.fill" : "mic.fill", disabled: !model.audioConnected, action: model.toggleMute)
                    callControl("Hang up", icon: "phone.down.fill", destructive: true, disabled: model.state?.occupied == true, action: model.hangup)
                }
                if !dynamicTypeSize.isAccessibilitySize { Text("Audio ends. Your agents keep working.").font(.caption).foregroundStyle(.secondary) }
            } else {
                Button { Task { await model.call() } } label: {
                    Label(dynamicTypeSize.isAccessibilitySize ? "Call" : "Call Codex Voice", systemImage: "phone.fill").font(.headline).frame(maxWidth: .infinity, minHeight: 48)
                }.buttonStyle(.borderedProminent).accessibilityLabel("Call Codex Voice").disabled(!model.canCall)
                if !dynamicTypeSize.isAccessibilitySize { Text("Your conversation and agent work stay in the same session.").font(.caption).foregroundStyle(.secondary).multilineTextAlignment(.center) }
            }
        }.frame(maxWidth: 640).padding(.horizontal, 24).padding(.vertical, 16).frame(maxWidth: .infinity).background(.bar)
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
                    LabeledContent("Audio", value: connectionTitle)
                    Text("The microphone sends audio to Codex. No on-device speech recognition is used.")
                    Text("Leaving the app ends audio. Return and call again to continue with the same orchestrator.")
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

struct VoiceOperatorEntry: View {
    @ObservedObject var model: AgenthailIOSModel
    @State private var presented = false
    @State private var operatorID: String?

    var body: some View {
        Button("Talk to orchestrator", systemImage: "waveform") { presented = true }
            .font(.subheadline.weight(.semibold))
            .buttonStyle(.borderedProminent)
            .controlSize(.large)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(minHeight: 56)
            .accessibilityIdentifier("voice-entry")
            .sheet(isPresented: $presented) {
                AgenthailVoiceOperatorSheet { id in operatorID = id }
                    .sheet(isPresented: Binding(get: { operatorID != nil }, set: { if !$0 { operatorID = nil } })) {
                        if let id = operatorID {
                            NavigationStack {
                                SessionRouteView(model: model, sessionID: id)
                                    .toolbar { ToolbarItem(placement: .topBarTrailing) { Button("Back to Voice") { operatorID = nil } } }
                            }
                        }
                    }
            }
            .onChange(of: model.isPaired) { _, paired in if !paired { presented = false } }
    }
}
