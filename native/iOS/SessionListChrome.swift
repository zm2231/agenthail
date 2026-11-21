import SwiftUI
import UIKit

/// What the ring around a session's agent says at a glance. The row says the
/// same thing in words, in its status line and to VoiceOver.
enum SessionRing: Equatable {
    case needsYou, unavailable, working, background, finished, idle

    static func kind(_ session: SessionState, needsYou: Bool, finished: Bool = false) -> SessionRing {
        if needsYou { return .needsYou }
        switch SessionDisplayKind.of(session) {
        case .unavailable: return .unavailable
        case .waiting: return .needsYou
        case .working: return .working
        case .finishing: return .background
        default: return finished ? .finished : .idle
        }
    }

    var spoken: String? {
        switch self {
        case .needsYou: "Needs you"
        case .unavailable: "Unavailable"
        case .working: "Working"
        case .background: "Waiting on background work"
        case .finished: "Finished"
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
                .foregroundStyle(PhonePalette.muted)
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
        case .finished:
            Circle().stroke(PhonePalette.done, lineWidth: 2.5)
        case .idle:
            EmptyView()
        }
    }
}

struct SessionScopeChips: View {
    @Binding var scope: SessionFilter
    let counts: [SessionFilter: Int]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(SessionFilter.allCases) { filter in
                    chip(filter)
                }
            }
            .padding(.vertical, 2)
        }
        .scrollClipDisabled()
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Sessions to show")
    }

    private func chip(_ filter: SessionFilter) -> some View {
        let selected = scope == filter
        let count = counts[filter] ?? 0
        let tint = filter == .running && count > 0 ? PhonePalette.working : PhonePalette.muted
        return Button { scope = filter } label: {
            HStack(spacing: 6) {
                Text(filter.rawValue)
                Text("\(count)")
                    .monospacedDigit()
                    .foregroundStyle(selected ? AnyShapeStyle(Color.white) : AnyShapeStyle(tint))
            }
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(selected ? Color.white : Color.primary)
            .padding(.horizontal, 14)
            .frame(minHeight: 44)
            .background {
                if selected { Capsule().fill(PhonePalette.accent) }
            }
            .floatingGlass(in: Capsule(), interactive: true, lifted: false)
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("\(filter.rawValue), \(count)")
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("scope-" + String(describing: filter))
    }
}

/// Opens the voice sheet from the Sessions toolbar, so it never sits over the
/// list or the tab bar.
struct VoiceToolbarButton: View {
    var model: AgenthailIOSModel
    @EnvironmentObject private var voiceCalls: VoiceCallCoordinator
    @State private var presented = false

    var body: some View {
        let live = voiceCalls.model?.hasLocalCall == true
        Button {
            voiceCalls.open()
            presented = true
        } label: {
            Image(systemName: live ? "phone.fill" : "waveform")
                .foregroundStyle(live ? AnyShapeStyle(PhonePalette.working) : AnyShapeStyle(Color.primary))
                .frame(minWidth: 44, minHeight: 44)
                .contentShape(Rectangle())
        }
        .accessibilityLabel(live ? "Return to call" : "Talk to orchestrator")
        .accessibilityIdentifier("voice-entry")
        .sheet(isPresented: $presented) {
            if let call = voiceCalls.model {
                AgenthailVoiceOperatorSheet(model: call, rowMemory: model.rowMemory) { model.openSession(model.voiceSessionID($0)) }
            }
        }
        .onChange(of: model.isPaired) { _, paired in if !paired { presented = false } }
    }
}

@MainActor
enum PresentedSheets {
    private static var drafts: Set<UUID> = []
    private static var closeWhenDraftsClose = false

    // A sheet whose input differs from what it opened with is never closed by a
    // route: the session opens underneath, and the other sheets close once the
    // last such sheet does.
    static func closeAll() {
        guard drafts.isEmpty else {
            closeWhenDraftsClose = true
            return
        }
        closeWhenDraftsClose = false
        for case let scene as UIWindowScene in UIApplication.shared.connectedScenes {
            for window in scene.windows where window.isKeyWindow {
                guard let root = window.rootViewController, root.presentedViewController != nil else { continue }
                root.dismiss(animated: true)
            }
        }
    }

    fileprivate static func set(_ id: UUID, holding: Bool) {
        if holding { drafts.insert(id) } else { drafts.remove(id) }
    }

    fileprivate static func closed(_ id: UUID) {
        drafts.remove(id)
        if drafts.isEmpty, closeWhenDraftsClose { closeAll() }
    }
}

private struct HoldsDraft: ViewModifier {
    let dirty: Bool
    @State private var id = UUID()

    func body(content: Content) -> some View {
        content
            .onChange(of: dirty, initial: true) { _, dirty in PresentedSheets.set(id, holding: dirty) }
            .onDisappear { PresentedSheets.closed(id) }
    }
}

extension View {
    func holdsDraft(_ dirty: Bool) -> some View {
        modifier(HoldsDraft(dirty: dirty))
    }
}
