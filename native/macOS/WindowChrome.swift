import AppKit
import SwiftUI

private struct WindowChromeTopKey: EnvironmentKey {
    static let defaultValue: CGFloat = 0
}

extension EnvironmentValues {
    /// The height of the title-bar strip drawn over the top of the window.
    var windowChromeTop: CGFloat {
        get { self[WindowChromeTopKey.self] }
        set { self[WindowChromeTopKey.self] = newValue }
    }
}

extension View {
    /// Starts a column below the title-bar strip when the window has one.
    func belowWindowChrome() -> some View {
        modifier(BelowWindowChrome())
    }
}

private struct BelowWindowChrome: ViewModifier {
    @Environment(\.windowChromeTop) private var top

    func body(content: Content) -> some View {
        content
            .padding(.top, top)
            .ignoresSafeArea(.container, edges: top > 0 ? .top : [])
    }
}

struct TitleBarStrip: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @ObservedObject var interface: InterfaceStore
    @ObservedObject private var shortcuts = ShortcutStore.shared

    static let height: CGFloat = 40
    static let buttonsWidth: CGFloat = 84
    private static let minimumTab: CGFloat = 120
    private static let maximumTab: CGFloat = 240

    var body: some View {
        let tree = model.sessionTree
        HStack(spacing: DesktopSpace.s) {
            GeometryReader { geometry in
                let tabs = pane.tabs.tabs
                let fits = max(Int(geometry.size.width / Self.minimumTab), 1)
                let width = min(max(geometry.size.width / CGFloat(max(tabs.count, 1)), Self.minimumTab), Self.maximumTab)
                HStack(spacing: 2) {
                    ScrollViewReader { reader in
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 2) {
                                ForEach(tabs) { tab in
                                    SessionTabView(model: model, pane: pane, tab: tab, label: interface.config.tabLabel)
                                        .frame(width: width)
                                        .id(tab.id)
                                }
                            }
                        }
                        .onChange(of: pane.tabs.active) { _, active in
                            if let active { withAnimation { reader.scrollTo(active) } }
                        }
                    }
                    if tabs.count > fits {
                        Menu {
                            ForEach(tabs) { tab in
                                Button(SessionTabView.title(model.knownSessions.first { $0.id == tab.id }, id: tab.id, label: interface.config.tabLabel)) { pane.activateTab(tab.id) }
                            }
                        } label: {
                            Text("\(tabs.count - fits) more")
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                    }
                    Button { model.openNewSession() } label: { Image(systemName: "plus") }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, DesktopSpace.s)
                        .help(shortcuts.help("New session", .newSession))
                        .accessibilityLabel("New session")
                    Spacer(minLength: 0)
                }
                .frame(maxHeight: .infinity)
            }
            if !tree.needsYou.isEmpty {
                NeedsYouPill(sessions: tree.needsYou) { pane.select($0) }
            }
            Button { model.paletteVisible.toggle() } label: { Image(systemName: "magnifyingglass") }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .help(shortcuts.help("Go to session", .goToSession))
                .accessibilityLabel("Go to session")
        }
        .font(.system(size: DesktopType.body))
        .padding(.leading, Self.buttonsWidth)
        .padding(.trailing, DesktopSpace.l)
        .frame(height: Self.height)
        .glassPane("titlebar", .chrome, fallback: DesktopPalette.side)
        .background {
            Button("") { pane.stepTab(1) }.keyboardShortcut(.tab, modifiers: .control).hidden()
            Button("") { pane.stepTab(-1) }.keyboardShortcut(.tab, modifiers: [.control, .shift]).hidden()
        }
    }
}

extension TitleBarStrip {
    /// Centres the close, minimise and zoom buttons on the strip.
    static func placeWindowButtons(in window: NSWindow) {
        let kinds: [NSWindow.ButtonType] = [.closeButton, .miniaturizeButton, .zoomButton]
        for (index, kind) in kinds.enumerated() {
            guard let button = window.standardWindowButton(kind), let container = button.superview else { continue }
            let y = container.isFlipped ? (height - button.frame.height) / 2 : container.bounds.height - (height + button.frame.height) / 2
            button.setFrameOrigin(NSPoint(x: 18 + CGFloat(index) * 20, y: y))
        }
    }
}

/// Says a tab closed because its session was archived elsewhere, and offers
/// to bring it back.
struct ArchivedTabNotice: View {
    @ObservedObject var pane: SessionPane
    let session: SessionState
    let label: String

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text("\(SessionTabView.title(session, id: session.id, label: label)) was archived")
                    .foregroundStyle(DesktopPalette.text)
                if let error = pane.archivedTabError {
                    Text(error)
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.failed)
                }
            }
            .lineLimit(1)
            .truncationMode(.middle)
            Button(pane.archivedTabError == nil ? "Unarchive" : "Try Again") { [session] in Task { await pane.unarchiveTab(session) } }
            Button { pane.archivedTab = nil } label: {
                Image(systemName: "xmark").font(.system(size: 9, weight: .semibold))
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .accessibilityLabel("Dismiss")
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 36)
        .frame(maxWidth: 480)
        .floatingGlass(DesktopPalette.raised, in: Capsule())
        .task(id: "\(session.id)/\(pane.archivedTabError ?? "")") {
            guard pane.archivedTabError == nil else { return }
            try? await Task.sleep(for: .seconds(10))
            if !Task.isCancelled, pane.archivedTab?.id == session.id, pane.archivedTabError == nil { pane.archivedTab = nil }
        }
    }
}

struct SessionTabView: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let tab: SessionTabs.Tab
    let label: String
    @Environment(\.openWindow) private var openWindow
    @State private var hovering = false

    static func title(_ session: SessionState?, id: String, label: String) -> String {
        if id == SessionTabs.newSession { return "New Session" }
        guard let session else { return id }
        let alias = session.alias.flatMap { $0.isEmpty ? nil : "@\($0)" }
        let trimmed = session.name.trimmingCharacters(in: .whitespacesAndNewlines)
        let name = trimmed.isEmpty || trimmed == session.id || UUID(uuidString: trimmed) != nil ? nil : trimmed
        let parts: [String?]
        switch label {
        case "alias": parts = [alias ?? name]
        case "title": parts = [name ?? alias]
        default: parts = [alias, name]
        }
        let title = parts.compactMap { $0 }.joined(separator: " ")
        return title.isEmpty ? "Untitled conversation" : title
    }

    var body: some View {
        let session = model.knownSessions.first { $0.id == tab.id }
        let active = pane.tabs.active == tab.id
        let needsYou = model.sessionTree.attention[tab.id] != nil
        let composing = tab.id == SessionTabs.newSession
        HStack(spacing: 6) {
            if composing {
                Image(systemName: "square.and.pencil")
                    .font(.system(size: 10))
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityHidden(true)
            } else {
                Circle()
                    .fill(session.map { DesktopPalette.statusColor($0, needsYou: needsYou, finishedUnseen: model.finishedUnseen.contains($0.id)) } ?? DesktopPalette.muted)
                    .frame(width: 7, height: 7)
                    .accessibilityHidden(true)
            }
            Text(Self.title(session, id: tab.id, label: label))
                .italic(!tab.kept)
                .foregroundStyle(active ? DesktopPalette.text : DesktopPalette.text2)
                .lineLimit(1)
                .truncationMode(.tail)
            if let host = session?.remoteHostName {
                Text(host)
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1)
                    .overlay(RoundedRectangle(cornerRadius: 4).strokeBorder(DesktopPalette.line))
                    .lineLimit(1)
                    .fixedSize()
            }
            Spacer(minLength: 0)
            Button { pane.closeTab(tab.id) } label: {
                Image(systemName: "xmark").font(.system(size: 9, weight: .semibold))
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .opacity(active || hovering ? 1 : 0)
            .accessibilityLabel("Close tab")
        }
        .padding(.horizontal, 10)
        .frame(height: 28)
        .background {
            if active { Color.clear.floatingGlass(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.card)) }
        }
        .contentShape(Rectangle())
        .onTapGesture(count: 2) { pane.keepTab(tab.id) }
        .onTapGesture { pane.activateTab(tab.id) }
        .onHover { hovering = $0 }
        .contextMenu {
            if !composing {
                if !tab.kept { Button("Keep Open") { pane.keepTab(tab.id) } }
                Button("Open in New Window") {
                    pane.closeTab(tab.id)
                    openWindow(id: "session", value: tab.id)
                }
                Divider()
            }
            Button("Close Tab") { pane.closeTab(tab.id) }
            Button("Close Other Tabs") { pane.closeOtherTabs(tab.id) }
                .disabled(pane.tabs.tabs.count < 2)
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(active ? [.isSelected, .isButton] : .isButton)
        .help(session.map { Self.title($0, id: tab.id, label: "alias-title") } ?? "")
    }
}

private struct NeedsYouPill: View {
    let sessions: [SessionState]
    let open: (String) -> Void

    var body: some View {
        Menu {
            ForEach(sessions) { session in
                Button(SessionTabView.title(session, id: session.id, label: "alias-title")) { open(session.id) }
            }
        } label: {
            HStack(spacing: 6) {
                Circle().fill(DesktopPalette.attention).frame(width: 7, height: 7)
                Text("\(sessions.count) need\(sessions.count == 1 ? "s" : "") you")
                    .foregroundStyle(DesktopPalette.attention)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .overlay(Capsule().strokeBorder(DesktopPalette.attentionLine))
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .accessibilityLabel("\(sessions.count) sessions need you")
    }
}

struct StatusStrip: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @ObservedObject var interface: InterfaceStore
    @StateObject private var usage = UsageMonitor()

    static let height: CGFloat = 26

    var body: some View {
        let items = interface.config.statusItems
        let sessions = model.knownSessions.filter { $0.archived != true }
        let working = sessions.filter(\.isWorking).count
        let needsYou = model.sessionTree.needsYou
        HStack(spacing: DesktopSpace.l) {
            if items.contains("usage") { UsageLimits(usage: usage) }
            Spacer(minLength: DesktopSpace.l)
            ForEach(items.filter { $0 != "usage" }, id: \.self) { item in
                switch item {
                case "working":
                    Label { Text("\(working) working") } icon: { Circle().fill(DesktopPalette.working).frame(width: 7, height: 7) }
                case "needs-you":
                    Button { if let first = needsYou.first { pane.select(first.id) } } label: {
                        Label { Text("\(needsYou.count) need\(needsYou.count == 1 ? "s" : "") you") } icon: { Circle().fill(DesktopPalette.attention).frame(width: 7, height: 7) }
                    }
                    .buttonStyle(.plain)
                    .disabled(needsYou.isEmpty)
                case "hosts":
                    Label(hosts, systemImage: "desktopcomputer")
                case "terminal":
                    let session = pane.displayedSession
                    Button { if let session { model.focusInTerminal(session) } } label: {
                        Label("Terminal", systemImage: "terminal")
                    }
                    .buttonStyle(.plain)
                    .disabled(session?.runtime?.focusable != true)
                    .help("Show this session's terminal")
                default:
                    EmptyView()
                }
            }
        }
        .labelStyle(StatusLabelStyle())
        .font(.system(size: DesktopType.meta))
        .foregroundStyle(DesktopPalette.text2)
        .padding(.horizontal, DesktopSpace.l)
        .frame(height: Self.height)
        .glassPane("statusbar", .chrome, fallback: DesktopPalette.side)
        .task(id: model.isConnected) {
            guard items.contains("usage"), model.isConnected else { return }
            await usage.run(model)
        }
    }

    private var hosts: String {
        let remote = (model.snapshot?.daemon.hosts ?? []).filter { $0.state == "connected" }.map(\.name)
        return (["this Mac"] + remote).joined(separator: " · ")
    }
}

private struct StatusLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 5) {
            configuration.icon
            configuration.title
        }
    }
}

private struct UsageLimits: View {
    @ObservedObject var usage: UsageMonitor

    var body: some View {
        TimelineView(.everyMinute) { context in
            let meters = UsageMeters.meters(provider: "Claude", usage: usage.claude, now: context.date) + UsageMeters.meters(provider: "Codex", usage: usage.codex, now: context.date)
            HStack(spacing: DesktopSpace.l) {
                if meters.isEmpty {
                    Text(usage.problem ?? "Claude")
                }
                ForEach(meters, id: \.label) { meter in
                    HStack(spacing: 6) {
                        Text(meter.label)
                        ProgressView(value: min(max(meter.percent / 100, 0), 1))
                            .progressViewStyle(.linear)
                            .frame(width: 44)
                            .tint(meter.percent >= 90 ? DesktopPalette.failed : DesktopPalette.text2)
                        Text(meter.text)
                            .monospacedDigit()
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(meter.spokenLabel)
                }
            }
        }
    }
}

@MainActor
final class UsageMonitor: ObservableObject {
    @Published private(set) var claude: ProviderUsage?
    @Published private(set) var codex: ProviderUsage?
    @Published private(set) var problem: String?

    func run(_ model: AgenthailModel) async {
        while !Task.isCancelled {
            await load(model)
            try? await Task.sleep(for: .seconds(120))
        }
    }

    private func load(_ model: AgenthailModel) async {
        guard let api = model.api else { return }
        if model.snapshot?.surfaces.contains(where: { $0.name == "codex" && $0.connected }) == true {
            codex = try? await api.codexUsage().usage
        } else {
            codex = nil
        }
        do {
            let options = try await api.sessionOptions()
            guard let root = options.claudeConfigRoots?.first(where: \.primary) ?? options.claudeConfigRoots?.first else {
                claude = nil
                return
            }
            let fresh = try await api.claudeRootUsage(root: root.root)
            claude = fresh.usage ?? root.usage
            problem = fresh.usageProblem
        } catch {
            if error.isCancellation { return }
            problem = AgenthailAPIError.userMessage(error)
        }
    }
}
