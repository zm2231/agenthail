import AppKit
import SwiftUI

struct VoiceWindow: View {
    @ObservedObject var voice: VoiceController
    let openSession: (String) -> Void

    var body: some View {
        Group {
            if let call = voice.call {
                VoiceCallView(call: call, openSession: openSession)
            } else {
                Color.clear
            }
        }
        .frame(minWidth: 420, idealWidth: 460, minHeight: 480, idealHeight: 620)
        .background(DesktopPalette.window)
        .onAppear { if voice.call == nil { voice.route(to: "") } }
        .onDisappear { voice.close() }
    }
}

struct VoiceCallView: View {
    @ObservedObject var call: VoiceOperatorModel
    let openSession: (String) -> Void
    @Environment(\.openWindow) private var openWindow
    @State private var confirmTargetInterrupt = false
    @State private var confirmNewConversation = false

    private var routeIsEmpty: Bool { call.targetID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    private var callActive: Bool { call.state?.hasCall == true || call.audioConnected || call.dialing }

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    notices
                    transcript
                    if let session = call.state?.session {
                        HStack {
                            Label(call.detail?.session.status == "busy" ? "Orchestrator is working" : "Orchestrator", systemImage: "terminal")
                                .foregroundStyle(DesktopPalette.text2)
                            Spacer()
                            Button("Open timeline") {
                                openSession(session.id)
                                NSApplication.shared.activate()
                                openWindow(id: "main")
                            }
                                .buttonStyle(.link)
                        }
                        .font(.system(size: DesktopType.secondary))
                    }
                    if callActive, let id = call.state?.attemptId {
                        Text("Call ID \(id)")
                            .font(.system(size: DesktopType.caption).monospaced())
                            .foregroundStyle(DesktopPalette.muted)
                            .textSelection(.enabled)
                    }
                }
                .padding(DesktopSpace.xl)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            controls
        }
        .background(alignment: .bottomLeading) {
            if let bridge = call.audio as? VoiceAudioBridge {
                VoiceAudioSurface(bridge: bridge).frame(width: 1, height: 1).opacity(0).accessibilityHidden(true)
            }
        }
        .navigationTitle(call.state?.target == nil ? "Talk to orchestrator" : "Session voice")
        .confirmationDialog("Stop the selected session's confirmed active turn? Audio stays connected.", isPresented: $confirmTargetInterrupt) {
            Button("Stop selected session", role: .destructive) { Task { await call.interruptTarget() } }
        } message: {
            Text("This does not hang up the call or stop another agent.")
        }
        .confirmationDialog("Start a new orchestrator conversation?", isPresented: $confirmNewConversation, titleVisibility: .visible) {
            Button("Start new conversation") { Task { await call.startNewConversation() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This creates a separate Codex thread. The current conversation remains available in Sessions.")
        }
    }

    private var header: some View {
        HStack(spacing: DesktopSpace.m) {
            Image(systemName: call.audioConnected ? "waveform" : "waveform.slash")
                .font(.system(size: 18))
                .foregroundStyle(call.audioConnected ? DesktopPalette.accent : DesktopPalette.text2)
                .frame(width: 36, height: 36)
                .background(DesktopPalette.bubble, in: Circle())
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 2) {
                Text(call.connectionTitle)
                    .font(.system(size: DesktopType.body, weight: .semibold))
                    .foregroundStyle(DesktopPalette.text)
                    .accessibilityIdentifier("voice-status")
                Text(call.state?.target.map { "Routed to \($0.name)" } ?? "Codex Voice · Agenthail orchestrator")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer()
            if call.working || call.dialing { ProgressView().controlSize(.small) }
            Button("New conversation", systemImage: "square.and.pencil") { confirmNewConversation = true }
                .labelStyle(.iconOnly)
                .buttonStyle(.borderless)
                .help("New conversation")
                .disabled(!call.canStartNewConversation)
                .accessibilityIdentifier("new-voice-conversation")
        }
        .padding(.horizontal, DesktopSpace.xl)
        .padding(.top, 36)
        .padding(.bottom, 14)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
    }

    @ViewBuilder private var notices: some View {
        if call.microphoneDenied {
            VStack(alignment: .leading, spacing: DesktopSpace.s) {
                Label("Microphone access is off for Agenthail", systemImage: "mic.slash.fill")
                    .font(.system(size: DesktopType.body, weight: .semibold))
                    .foregroundStyle(DesktopPalette.failed)
                Text("Turn on Agenthail in System Settings, Privacy & Security, Microphone, then call again.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text)
                Button("Open System Settings") { VoicePrivacySettings.openMicrophone() }
            }
            .padding(DesktopSpace.m)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(DesktopPalette.failedBackground, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
            .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).stroke(DesktopPalette.failedLine))
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("voice-microphone-denied")
        } else if let error = call.error {
            Text(error)
                .font(.system(size: DesktopType.body))
                .foregroundStyle(DesktopPalette.failed)
                .textSelection(.enabled)
                .accessibilityIdentifier("voice-error")
        }
        if let connectionError = call.connectionError {
            Text(connectionError).font(.system(size: DesktopType.body)).foregroundStyle(DesktopPalette.failed)
        }
        if let message = call.state?.message, !message.isEmpty {
            Text(message).font(.system(size: DesktopType.body)).foregroundStyle(DesktopPalette.failed).textSelection(.enabled)
        }
        if call.state?.occupied == true {
            Text("Another device owns this call. Hang up there to call from this Mac.")
                .font(.system(size: DesktopType.body))
                .foregroundStyle(DesktopPalette.text2)
        }
    }

    @ViewBuilder private var transcript: some View {
        if let state = call.state, !state.transcripts.isEmpty {
            ForEach(state.transcripts) { item in
                VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                    Text(item.role == "user" ? "You" : (state.target == nil ? "Orchestrator" : "Selected session"))
                        .font(.system(size: DesktopType.meta, weight: .medium))
                        .foregroundStyle(DesktopPalette.text2)
                    Text(item.text)
                        .font(.system(size: DesktopType.body))
                        .foregroundStyle(DesktopPalette.text)
                        .textSelection(.enabled)
                }
            }
        } else {
            VStack(alignment: .leading, spacing: 6) {
                Text("What would you like to work on?")
                    .font(.system(size: DesktopType.title, weight: .semibold))
                    .foregroundStyle(DesktopPalette.text)
                Text("Talk to the Agenthail orchestrator, or route this call to one exact existing Codex or Claude session.")
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
            }
            .padding(.vertical, DesktopSpace.s)
        }
    }

    @ViewBuilder private var controls: some View {
        VStack(alignment: .leading, spacing: 10) {
            if call.hostHangupPending {
                Label("Audio ended locally. Confirming host hangup…", systemImage: "hourglass")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            } else if call.hostHangupUnconfirmed {
                Text("Audio is off locally. The host did not confirm hangup.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                Button("Try hangup again", role: .destructive, action: call.hangup)
                    .disabled(call.state?.occupied == true)
            } else {
                VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                    Text("Session route (optional)")
                        .font(.system(size: DesktopType.meta, weight: .medium))
                        .foregroundStyle(DesktopPalette.text2)
                    TextField("Exact session ID or alias", text: $call.targetID)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityIdentifier("voice-route")
                    Text(routeIsEmpty ? "Leave blank to use Agenthail's normal orchestration workflow." : "This routes work only to that exact existing session.")
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                }
                if callActive {
                    HStack(spacing: DesktopSpace.s) {
                        if call.audioConnected && call.state?.occupied != true {
                            Button(routeIsEmpty ? "Return to orchestrator" : "Transfer to session") { Task { await call.transfer() } }
                                .disabled(call.working || call.state?.phase != "connected")
                            if call.state?.target != nil {
                                Button("Stop selected session") { confirmTargetInterrupt = true }
                                    .disabled(call.working || call.state?.phase != "connected")
                            }
                        }
                        Spacer()
                        Button(call.muted ? "Unmute" : "Mute", systemImage: call.muted ? "mic.slash.fill" : "mic.fill", action: call.toggleMute)
                            .disabled(!call.audioConnected)
                        Button("Hang up", systemImage: "phone.down.fill", role: .destructive, action: call.hangup)
                            .disabled(call.state?.occupied == true)
                            .keyboardShortcut(.cancelAction)
                    }
                    Text("Audio ends. Your agents keep working.")
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                } else {
                    Button {
                        Task { await call.call() }
                    } label: {
                        Label(routeIsEmpty ? "Talk to orchestrator" : "Call session", systemImage: "phone.fill")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(DesktopPalette.accent)
                    .controlSize(.large)
                    .keyboardShortcut(.defaultAction)
                    .disabled(!call.canCall)
                    .accessibilityIdentifier("voice-call")
                }
            }
        }
        .padding(DesktopSpace.xl)
        .background(DesktopPalette.dock)
        .overlay(alignment: .top) { Rectangle().fill(DesktopPalette.line).frame(height: 1) }
    }
}
