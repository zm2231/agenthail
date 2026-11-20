import SwiftUI

struct ConversationHeader: View {
    let session: SessionState
    let title: String
    @Binding var inspectorVisible: Bool
    var leadingInset: CGFloat = 22
    var underTabs = false
    var inspectorHelp = "Inspector"
    var onFocusTerminal: () -> Void = {}
    var onCall: (() -> Void)?

    static let height = WindowButtons.bandHeight
    static let underTabsHeight: CGFloat = 32

    var body: some View {
        HStack(spacing: DesktopSpace.m) {
            HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                Text(title)
                    .font(.system(size: DesktopType.body, weight: .semibold))
                    .truncationMode(.middle)
                    .layoutPriority(1)
                if let detail = SessionFacts.headerDetail(session) {
                    Text(detail)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            .lineLimit(1)
            .frame(minWidth: Self.titleMinWidth, alignment: .leading)
            .accessibilityElement(children: .combine)
            Spacer(minLength: DesktopSpace.m)
            stateArea
            HStack(spacing: DesktopSpace.xs) {
                FocusSwitch()
                if let onCall {
                    Button(action: onCall) {
                        Image(systemName: "waveform")
                            .frame(width: 28, height: 28)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.text2)
                    .help("Call this session by voice")
                    .accessibilityLabel("Call this session")
                }
                Button {
                    inspectorVisible.toggle()
                } label: {
                    Image(systemName: "sidebar.right")
                        .frame(width: 28, height: 28)
                        .selectionMark(inspectorVisible)
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .help(inspectorHelp)
                .accessibilityLabel("Toggle inspector")
                .accessibilityValue(inspectorVisible ? "Shown" : "Hidden")
            }
        }
        .padding(.leading, leadingInset)
        .padding(.trailing, 14)
        .frame(height: underTabs ? Self.underTabsHeight : Self.height)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
    }

    static let titleMinWidth: CGFloat = 16

    private var stateArea: some View {
        TimelineView(.everyMinute) { context in
            stateFit(SessionDisplayKind.label(session, now: context.date))
        }
        .accessibilityIdentifier("conversation-header-state")
    }

    // The state gives up its neighbours, then its label, before anything is clipped; the label stays as help and for VoiceOver.
    private func stateFit(_ state: String) -> some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: DesktopSpace.m) {
                stateLabel(state)
                liveFacts
                openInHost
            }
            HStack(spacing: DesktopSpace.m) {
                stateLabel(state)
                openInHost
            }
            HStack(spacing: DesktopSpace.m) {
                stateLabel(state)
                openInHostIcon
            }
            HStack(spacing: DesktopSpace.m) {
                stateDot(state)
                openInHostIcon
            }
            stateDot(state)
        }
    }

    private func stateDot(_ state: String) -> some View {
        StatusIndicator(session: session, needsYou: false)
            .help(state)
            .accessibilityElement()
            .accessibilityLabel(state)
    }

    private func stateLabel(_ state: String) -> some View {
        HStack(spacing: 6) {
            StatusIndicator(session: session, needsYou: false)
            Text(state)
        }
        .font(.system(size: DesktopType.secondary))
        .foregroundStyle(DesktopPalette.text2)
        .lineLimit(1)
        .fixedSize()
    }

    @ViewBuilder private var liveFacts: some View {
        let live = SessionFacts.headerFacts(session).joined(separator: " · ")
        if !live.isEmpty {
            Text(live)
                .font(.system(size: DesktopType.secondary).monospacedDigit())
                .foregroundStyle(DesktopPalette.text2)
                .lineLimit(1)
                .fixedSize()
        }
    }

    @ViewBuilder private var openInHost: some View {
        if session.runtime?.focusable == true, let host = session.runtime?.hostName {
            Button("Open in \(host)") { onFocusTerminal() }
                .buttonStyle(.plain)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.accentText)
                .help("Bring this session's terminal to the front")
                .fixedSize()
        }
    }

    @ViewBuilder private var openInHostIcon: some View {
        if session.runtime?.focusable == true, let host = session.runtime?.hostName {
            Button { onFocusTerminal() } label: {
                Image(systemName: "terminal")
                    .frame(width: 28, height: 28)
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .help("Open in \(host)")
            .accessibilityLabel("Open in \(host)")
        }
    }
}
