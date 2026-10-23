import SwiftUI

extension VoiceOperatorModel {
    static func paired(targetID: String = "") -> VoiceOperatorModel {
        let paired = PairedCredential.all()
        let chosen = UserDefaults.standard.string(forKey: AgenthailIOSModel.voiceHostKey)
        guard let credential = paired.first(where: { $0.hostID == chosen }) ?? paired.first else {
            return VoiceOperatorModel(targetID: targetID, unavailable: "Pair this phone with Agenthail on your computer first.")
        }
        return VoiceOperatorModel(endpoint: credential.endpoint, token: credential.token, targetID: targetID, diagnostics: VoiceCallDiagnostics())
    }
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
    @State private var showActivity = false
    @State private var followingLatest = true
    @State private var atLatest = true
    @State private var userScrolling = false
    let openSession: (String) -> Void

    init(model: VoiceOperatorModel, openSession: @escaping (String) -> Void) {
        self.model = model
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
                            Button("Speak next request") { VoiceEvaluation.device.speakNext() }
                                .font(.caption).disabled(!model.audioConnected)
                        }
                    }
#endif
                    HStack(spacing: 12) {
                        Image(systemName: model.audioConnected ? "waveform" : "waveform.slash")
                            .font(.title2).foregroundStyle(model.audioConnected ? Color.accentColor : Color.secondary)
                            .frame(width: 44, height: 44).background(.quaternary, in: Circle())
                        VStack(alignment: .leading, spacing: 4) {
                            Text(model.connectionTitle).font(.headline)
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
                            VStack(alignment: .leading, spacing: 8) {
                                Text(item.role == "user" ? "You" : (model.state?.target == nil ? "Orchestrator" : "Selected session")).font(.subheadline.weight(.medium)).foregroundStyle(.secondary)
                                Text(item.text).font(.body).lineSpacing(4).textSelection(.enabled)
                            }
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
                        .accessibilityIdentifier("voice-activity")
                    }
                    Color.clear.frame(height: 1).id("voice-bottom")
                }.frame(maxWidth: 640).padding(.horizontal, 24).padding(.bottom, 24).frame(maxWidth: .infinity)
                .environment(\.revealRow) { id in Task { @MainActor in proxy.scrollTo(id, anchor: .top) } }
                }
                .defaultScrollAnchor(.bottom, for: .initialOffset)
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
                    if followingLatest, !showActivity { proxy.scrollTo("voice-bottom", anchor: .bottom) }
                }
                .onChange(of: showActivity) { _, expanded in
                    if expanded { followingLatest = false }
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
        let event = model.state?.events.last?.sequence ?? 0
        let item = model.detail?.timeline?.items.last?.id ?? ""
        return "\(event):\(item)"
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
                callControl("Try hangup again", icon: "phone.down.fill", destructive: true, disabled: model.state?.occupied == true, action: model.hangup)
                if !dynamicTypeSize.isAccessibilitySize { Text("Audio is off locally. The host did not confirm hangup.").font(.caption).foregroundStyle(.secondary) }
            } else if model.state?.hasCall == true || model.audioConnected || model.dialing {
                if model.audioConnected && model.state?.occupied != true {
                    VStack(alignment: .leading, spacing: 8) {
                        TextField("Session ID or alias (optional)", text: $model.targetID)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .padding(12).background(.quaternary, in: RoundedRectangle(cornerRadius: 12))
                        HStack {
                            Button(model.targetID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Return to orchestrator" : "Transfer to session") { Task { await model.transfer() } }
                                .buttonStyle(.bordered)
                                .disabled(model.working)
                            if model.state?.target != nil {
                                Button("Clear route") { model.targetID = "" }
                                    .buttonStyle(.borderless)
                                Button("Stop selected session", role: .destructive) { confirmTargetInterrupt = true }
                                    .buttonStyle(.borderless)
                                    .disabled(model.working)
                            }
                        }
                    }
                }
                HStack(alignment: .top, spacing: 32) {
                    callControl("Type", icon: "keyboard", disabled: !model.audioConnected) { showKeyboard.toggle() }
                    callControl(model.muted ? "Unmute" : "Mute", icon: model.muted ? "mic.slash.fill" : "mic.fill", disabled: !model.audioConnected, action: model.toggleMute)
                    callControl("Hang up", icon: "phone.down.fill", destructive: true, disabled: model.state?.occupied == true, action: model.hangup)
                }
                if !dynamicTypeSize.isAccessibilitySize { Text("Audio ends. Your agents keep working.").font(.caption).foregroundStyle(.secondary) }
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    TextField("Session ID or alias (optional)", text: $model.targetID)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .padding(12).background(.quaternary, in: RoundedRectangle(cornerRadius: 12))
                    Text(model.targetID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "Voice starts with the normal Agenthail orchestrator workflow." : "Voice uses Codex realtime for audio and sends work only to this exact existing session.")
                        .font(.caption).foregroundStyle(.secondary)
                }
                callEntry
            }
        }.frame(maxWidth: 640).padding(.horizontal, 24).padding(.vertical, 16).frame(maxWidth: .infinity).background(.bar)
    }

    // One call button stays in place while hangup is confirmed, so the
    // controls do not jump between the confirming and ready states.
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

    private var connectionDetails: some View {
        NavigationStack {
            List {
                Section("Connection") {
                    LabeledContent("Provider", value: "Codex Voice")
                    if let target = model.state?.target {
                        LabeledContent("Coding target", value: target.name)
                        LabeledContent("Coding provider", value: model.state?.codingProvider ?? target.surface)
                    }
                    if let provider = model.state?.audioProvider { LabeledContent("Audio provider", value: provider) }
                    LabeledContent("Audio", value: model.connectionTitle)
                    Text("The microphone sends audio to Codex. No on-device speech recognition is used.")
                    Text("The call stays active when you close this screen, switch apps or lock your phone. Use Hang up or the system call controls to end audio. Force-quitting the app ends the call.")
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
                if let attempt = model.diagnostics?.attempts.last {
                    Section("Call lifecycle") {
                        LabeledContent("Attempt") { Text(attempt.id).font(.caption.monospaced()).textSelection(.enabled) }
                        if let cause = attempt.terminalCause { LabeledContent("Ended because", value: cause) }
                        ForEach(attempt.milestones.sorted { $0.value < $1.value }, id: \.key) { name, seconds in
                            LabeledContent(name, value: String(format: "%.2fs", seconds)).font(.caption.monospaced())
                        }
                        if let failure = model.diagnostics?.persistenceError {
                            Text("Call diagnostics could not be saved: \(failure)").foregroundStyle(.red)
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
                    AgenthailVoiceOperatorSheet(model: call) { id in operatorID = model.voiceSessionID(id) }
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
