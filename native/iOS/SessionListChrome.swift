import SwiftUI

/// What the ring around a session's agent says at a glance. The row says the
/// same thing in words, in its status line and to VoiceOver.
enum SessionRing: Equatable {
    case needsYou, unavailable, working, background, idle

    static func kind(_ session: SessionState, needsYou: Bool) -> SessionRing {
        if needsYou || session.isWaitingOnYou { return .needsYou }
        if session.isUnavailable { return .unavailable }
        if session.isWorking { return .working }
        if session.isWaitingOnBackground { return .background }
        return .idle
    }

    var spoken: String? {
        switch self {
        case .needsYou: "Needs you"
        case .unavailable: "Unavailable"
        case .working: "Working"
        case .background: "Waiting on background work"
        case .idle: nil
        }
    }
}

struct SessionAvatar: View {
    let surface: String
    let ring: SessionRing
    @ScaledMetric(relativeTo: .body) private var size: CGFloat = 34
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    static func glyph(_ surface: String) -> String {
        switch surface {
        case "claude": "asterisk"
        case "codex": "chevron.left.forwardslash.chevron.right"
        default: "terminal"
        }
    }

    var body: some View {
        ZStack {
            Circle().fill(.quaternary)
            Image(systemName: Self.glyph(surface))
                .font(.system(size: size * 0.4, weight: .semibold))
                .foregroundStyle(.secondary)
            ringView.padding(-3.5)
        }
        .frame(width: size, height: size)
        .padding(3.5)
        .accessibilityHidden(true)
    }

    static func turn(at date: Date) -> Angle {
        .degrees(date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1.4) / 1.4 * 360)
    }

    @ViewBuilder
    private var ringView: some View {
        switch ring {
        case .needsYou:
            Circle().stroke(PhonePalette.attention, lineWidth: 2.5)
        case .unavailable:
            Circle().stroke(PhonePalette.failed, lineWidth: 2.5)
        case .background:
            Circle().stroke(PhonePalette.working, style: StrokeStyle(lineWidth: 2, dash: [3, 3]))
        case .working:
            if reduceMotion || ProcessInfo.processInfo.arguments.contains("--preview-session") {
                Circle().stroke(PhonePalette.working, lineWidth: 2.5)
            } else {
                // The angle comes from the clock, so every working ring turns in step and a
                // row the list has not laid out does no work.
                TimelineView(.animation(minimumInterval: 1.0 / 30)) { context in
                    Circle()
                        .trim(from: 0, to: 0.7)
                        .stroke(PhonePalette.working, style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
                        .rotationEffect(Self.turn(at: context.date))
                }
            }
        case .idle:
            EmptyView()
        }
    }
}

struct SessionScopeChips: View {
    @Binding var scope: SessionFilter
    let runningCount: Int

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(SessionFilter.allCases) { filter in
                    chip(filter)
                }
            }
            .padding(.vertical, 2)
        }
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Sessions to show")
    }

    private func chip(_ filter: SessionFilter) -> some View {
        let selected = scope == filter
        return Button { scope = filter } label: {
            HStack(spacing: 6) {
                Text(filter.rawValue)
                if filter == .running, runningCount > 0 {
                    Text("\(runningCount)")
                        .monospacedDigit()
                        .foregroundStyle(selected ? Color.white : PhonePalette.working)
                }
            }
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(selected ? Color.white : Color.primary)
            .padding(.horizontal, 14)
            .frame(minHeight: 44)
            .background {
                if selected { Capsule().fill(SessionStyle.accent) }
            }
            .floatingGlass(in: Capsule(), interactive: true)
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(filter == .running && runningCount > 0 ? "Running, \(runningCount)" : filter.rawValue)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("scope-" + filter.rawValue.lowercased())
    }
}

/// Opens the voice sheet from the Sessions toolbar, so it never sits over the
/// list or the tab bar.
struct VoiceToolbarButton: View {
    @ObservedObject var model: AgenthailIOSModel
    @EnvironmentObject private var voiceCalls: VoiceCallCoordinator
    @State private var presented = false
    @State private var operatorID: String?

    var body: some View {
        let live = voiceCalls.model?.hasLocalCall == true
        Button {
            voiceCalls.open()
            presented = true
        } label: {
            Image(systemName: live ? "phone.fill" : "waveform")
                .foregroundStyle(live ? PhonePalette.done : Color.primary)
                .frame(minWidth: 44, minHeight: 44)
                .contentShape(Rectangle())
        }
        .accessibilityLabel(live ? "Return to call" : "Talk to orchestrator")
        .accessibilityIdentifier("voice-entry")
        .sheet(isPresented: $presented) {
            if let call = voiceCalls.model {
                AgenthailVoiceOperatorSheet(model: call, rowMemory: model.rowMemory) { id in operatorID = model.voiceSessionID(id) }
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
