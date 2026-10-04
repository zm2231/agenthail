import AppKit
import SwiftUI
import Textual

struct DesktopWindow: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        NavigationSplitView {
            SessionSidebar(model: model)
                .navigationSplitViewColumnWidth(min: 248, ideal: 264, max: 280)
        } detail: {
            ConversationPane(model: model)
                .ignoresSafeArea(.container, edges: .top)
                .inspector(isPresented: $model.inspectorVisible) {
                    SessionInspector(model: model)
                        .ignoresSafeArea(.container, edges: .top)
                        .inspectorColumnWidth(min: 270, ideal: 284, max: 300)
                }
        }
        .background(DesktopPalette.window)
        .toolbar(removing: .sidebarToggle)
    }
}

struct SessionSidebar: View {
    @ObservedObject var model: AgenthailModel
    @State private var expandedProjects: Set<String> = []

    var body: some View {
        let tree = model.sessionTree
        VStack(spacing: 0) {
            HStack(spacing: 14) {
                ForEach(SessionFilter.allCases) { filter in
                    Button {
                        model.sessionFilter = filter
                    } label: {
                        HStack(spacing: 5) {
                            Text(filter.rawValue)
                                .fontWeight(model.sessionFilter == filter ? .semibold : .regular)
                                .foregroundStyle(model.sessionFilter == filter ? DesktopPalette.text : DesktopPalette.text2)
                            Text("\(tree.counts[filter] ?? 0)")
                                .font(.system(size: 11))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(model.sessionFilter == filter ? .isSelected : [])
                    .keyboardShortcut(KeyEquivalent(Character("\(filter.shortcut)")), modifiers: .command)
                }
                Spacer()
            }
            .font(.system(size: 12.5))
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 10)

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    if !tree.needsYou.isEmpty {
                        VStack(alignment: .leading, spacing: 2) {
                            SidebarCaption("Needs you")
                            ForEach(tree.needsYou) { session in
                                sessionButton(session, needsYou: true)
                            }
                        }
                    }
                    ForEach(tree.projects) { project in
                        let expanded = expandedProjects.contains(project.id)
                        let shown = expanded ? project : project.limited(to: SessionTree.collapsedSessionLimit, keeping: model.selectedSessionID)
                        VStack(alignment: .leading, spacing: 1) {
                            ProjectHeaderView(name: project.name)
                            ForEach(shown.checkouts) { checkout in
                                if project.checkouts.count > 1 || !checkout.isMain {
                                    CheckoutRowView(checkout: checkout)
                                }
                                ForEach(checkout.sessions) { session in
                                    sessionButton(session, needsYou: model.attentionSessionIDs.contains(session.id))
                                }
                            }
                            if project.sessionCount > shown.sessionCount || expanded && project.sessionCount > SessionTree.collapsedSessionLimit {
                                Button(expanded ? "Show less" : "Show \(project.sessionCount - shown.sessionCount) more") {
                                    if expanded { expandedProjects.remove(project.id) } else { expandedProjects.insert(project.id) }
                                }
                                .buttonStyle(.plain)
                                .font(.system(size: 12))
                                .foregroundStyle(DesktopPalette.text2)
                                .padding(.leading, 35)
                                .padding(.vertical, 4)
                            }
                        }
                    }
                    if tree.projects.isEmpty {
                        Text(model.sessionFilter == .running ? "Nothing is running." : "No sessions yet.")
                            .font(.system(size: 12.5))
                            .foregroundStyle(DesktopPalette.text2)
                            .padding(.horizontal, 8)
                    }
                }
                .padding(.horizontal, 8)
                .padding(.bottom, 16)
            }

            ConnectionFooter(model: model)
        }
        .background(DesktopPalette.side)
    }
}

extension SessionSidebar {
    private func sessionButton(_ session: SessionState, needsYou: Bool) -> some View {
        let selected = model.selectedSessionID == session.id
        return Button {
            model.selectSession(session.id)
        } label: {
            SessionRowView(session: session, needsYou: needsYou)
                .padding(.vertical, 6)
                .padding(.horizontal, 8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(selected ? DesktopPalette.selection : (needsYou ? DesktopPalette.warnBackground : Color.clear), in: RoundedRectangle(cornerRadius: 7))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

struct SidebarCaption: View {
    let text: String
    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text)
            .font(.system(size: 11, weight: .semibold))
            .foregroundStyle(DesktopPalette.text2)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
    }
}

private extension SessionFilter {
    var shortcut: Int {
        switch self {
        case .running: return 1
        case .recent: return 2
        case .all: return 3
        }
    }
}

struct ProjectHeaderView: View {
    let name: String

    var body: some View {
        HStack(spacing: 7) {
            Text(String(name.prefix(1)).uppercased())
                .font(.system(size: 10.5, weight: .bold))
                .foregroundStyle(.white)
                .frame(width: 18, height: 18)
                .background(DesktopPalette.accent, in: RoundedRectangle(cornerRadius: 5))
                .accessibilityHidden(true)
            Text(name)
                .font(.system(size: 12.5, weight: .semibold))
                .foregroundStyle(DesktopPalette.text)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
    }
}

struct CheckoutRowView: View {
    let checkout: SessionTree.Checkout

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.triangle.branch")
                .font(.system(size: 10))
            Text(checkout.label)
                .font(.system(size: 11, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
            if checkout.dirty {
                Circle().fill(DesktopPalette.amber).frame(width: 5, height: 5)
                    .accessibilityLabel("Uncommitted changes")
            }
        }
        .foregroundStyle(DesktopPalette.text2)
        .padding(.leading, 33)
        .padding(.trailing, 8)
        .padding(.top, 4)
        .padding(.bottom, 2)
    }
}

struct SessionRowView: View {
    let session: SessionState
    let needsYou: Bool

    var body: some View {
        HStack(spacing: 9) {
            StatusIndicator(session: session, needsYou: needsYou)
            Text(session.title)
                .lineLimit(1)
                .foregroundStyle(session.open || session.isWorking ? DesktopPalette.text : DesktopPalette.text2)
            Spacer(minLength: 4)
            Text(relativeAge(session.lastActive))
                .font(.system(size: 11))
                .foregroundStyle(DesktopPalette.text2)
        }
        .font(.system(size: 13))
        .padding(.leading, 6)
        .accessibilityElement(children: .combine)
        .accessibilityValue(needsYou ? "Needs you" : session.isWorking ? "Working" : "Idle")
    }
}

struct StatusIndicator: View {
    let session: SessionState
    let needsYou: Bool

    var body: some View {
        Group {
            if session.isWorking && !needsYou {
                ProgressView()
                    .controlSize(.mini)
                    .tint(DesktopPalette.work)
                    .scaleEffect(0.8)
            } else {
                Circle()
                    .fill(DesktopPalette.statusColor(session, needsYou: needsYou))
                    .frame(width: 7, height: 7)
            }
        }
        .frame(width: 12, height: 12)
        .accessibilityHidden(true)
    }
}

extension SessionState {
    var title: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || trimmed == id || UUID(uuidString: trimmed) != nil { return "Untitled conversation" }
        return trimmed
    }
}

struct ConnectionFooter: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(model.isConnected ? DesktopPalette.green : DesktopPalette.amber)
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)
            Text(model.isConnected ? "Connected · this Mac" : model.reconnecting ? "Reconnecting…" : "Offline")
            Spacer()
            SettingsLink {
                Image(systemName: "slider.horizontal.3")
            }
            .buttonStyle(.plain)
            .help("Settings ⌘,")
        }
        .font(.system(size: 11.5))
        .foregroundStyle(DesktopPalette.text2)
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .overlay(alignment: .top) { Rectangle().fill(DesktopPalette.line).frame(height: 1) }
    }
}

struct ConversationPane: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        if let session = model.selectedSession {
            VStack(spacing: 0) {
                ConversationHeader(session: session, model: model.detail?.model, inspectorVisible: $model.inspectorVisible)
                TranscriptView(model: model, session: session)
                    .safeAreaInset(edge: .bottom, spacing: 0) {
                        ComposerView(model: model, session: session)
                    }
            }
            .background(DesktopPalette.window)
        } else {
            ContentUnavailableView(model.isConnected ? "Select a session" : "Agenthail is not reachable", systemImage: model.isConnected ? "bubble.left.and.bubble.right" : "bolt.horizontal.circle", description: Text(model.connectionError ?? "Choose a session from the sidebar."))
                .background(DesktopPalette.window)
        }
    }
}

struct ConversationHeader: View {
    let session: SessionState
    let model: String?
    @Binding var inspectorVisible: Bool

    var body: some View {
        HStack(spacing: 12) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text(session.alias.map { _ in session.name.isEmpty ? session.title : session.name } ?? session.title)
                    .font(.system(size: 13.5, weight: .semibold))
                if let alias = session.alias, !alias.isEmpty {
                    Text("@\(alias)")
                        .font(.system(size: 12))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            .lineLimit(1)
            Spacer()
            HStack(spacing: 6) {
                StatusIndicator(session: session, needsYou: false)
                Text(session.isWorking ? "Working" : session.open ? "Idle" : "Closed")
            }
            .font(.system(size: 12))
            .foregroundStyle(DesktopPalette.text2)
            Text([session.surface.capitalized, model].compactMap { $0 }.joined(separator: " · "))
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
            Button {
                inspectorVisible.toggle()
            } label: {
                Image(systemName: "sidebar.right")
                    .frame(width: 30, height: 30)
                    .background(inspectorVisible ? DesktopPalette.selection : Color.clear, in: RoundedRectangle(cornerRadius: 7))
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .keyboardShortcut("i", modifiers: [.command, .option])
            .help("Inspector ⌥⌘I")
            .accessibilityLabel("Toggle inspector")
            .accessibilityValue(inspectorVisible ? "Shown" : "Hidden")
        }
        .padding(.leading, 22)
        .padding(.trailing, 14)
        .frame(height: 52)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
    }
}

struct TranscriptView: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState

    var body: some View {
        let blocks = TranscriptBlock.build(model.detail?.timeline?.items ?? [])
        let sends = model.localSends[session.id] ?? []
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if let error = model.detail?.readError {
                    Text(error)
                        .font(.system(size: 12.5))
                        .foregroundStyle(DesktopPalette.text2)
                }
                ForEach(blocks) { block in
                    TranscriptBlockView(block: block)
                }
                ForEach(sends) { send in
                    UserBubble(text: send.text, receipt: send.status)
                }
                if session.isWorking {
                    HStack(spacing: 8) {
                        StatusIndicator(session: session, needsYou: false)
                        Text("Working")
                    }
                    .font(.system(size: 12.5))
                    .foregroundStyle(DesktopPalette.text2)
                }
            }
            .frame(maxWidth: 700)
            .padding(.horizontal, 24)
            .padding(.top, 28)
            .padding(.bottom, 12)
            .frame(maxWidth: .infinity)
        }
        .defaultScrollAnchor(.bottom)
        .overlay {
            if model.detail == nil, model.selectedSessionID == session.id {
                ProgressView().controlSize(.small)
            }
        }
    }
}

struct TranscriptBlock: Identifiable, Equatable {
    enum Kind: Equatable {
        case user(String)
        case assistant(String)
        case tools([TimelineItem])
    }

    let id: String
    let kind: Kind

    static func build(_ items: [TimelineItem]) -> [TranscriptBlock] {
        var blocks: [TranscriptBlock] = []
        var tools: [TimelineItem] = []
        func flushTools() {
            guard let first = tools.first else { return }
            blocks.append(TranscriptBlock(id: "tools-\(first.id)", kind: .tools(tools)))
            tools = []
        }
        for item in items {
            switch item.kind {
            case "message", "text":
                flushTools()
                guard !item.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { continue }
                blocks.append(TranscriptBlock(id: item.id, kind: item.role == "user" ? .user(item.text) : .assistant(item.text)))
            case "context", "done", "phase":
                continue
            default:
                tools.append(item)
            }
        }
        flushTools()
        return blocks
    }
}

struct TranscriptBlockView: View {
    let block: TranscriptBlock
    @State private var expanded = false

    var body: some View {
        switch block.kind {
        case .user(let text):
            UserBubble(text: text, receipt: nil)
        case .assistant(let text):
            StructuredText(markdown: text)
                .font(.system(size: 15.5, design: .serif))
                .lineSpacing(4)
                .foregroundStyle(DesktopPalette.text)
                .textSelection(.enabled)
        case .tools(let items):
            ToolRunView(items: items, expanded: $expanded)
        }
    }
}

struct UserBubble: View {
    let text: String
    let receipt: String?

    var body: some View {
        let envelope = PeerEnvelope(text)
        VStack(alignment: .trailing, spacing: 5) {
            if let sender = envelope.sender {
                Text("From \(sender)")
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Text(envelope.body)
                .font(.system(size: 14))
                .lineSpacing(3)
                .textSelection(.enabled)
                .padding(.horizontal, 13)
                .padding(.vertical, 9)
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: 14))
            if let receipt {
                Text(receipt)
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.text2)
            }
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .padding(.leading, 120)
    }
}

struct ToolRunView: View {
    let items: [TimelineItem]
    @Binding var expanded: Bool

    var body: some View {
        let calls = items.filter { $0.kind == "toolCall" || $0.kind == "tool" || $0.kind == "command" }
        let failed = items.filter { $0.status == "failed" || $0.status == "error" }.count
        VStack(alignment: .leading, spacing: 6) {
            Button {
                expanded.toggle()
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 10, weight: .semibold))
                    Text(calls.isEmpty ? "Thought" : calls.count == 1 ? "Ran 1 tool" : "Ran \(calls.count) tools")
                    if failed > 0 {
                        Text("· \(failed) failed").foregroundStyle(DesktopPalette.red)
                    }
                }
                .font(.system(size: 12.5))
                .foregroundStyle(DesktopPalette.text2)
            }
            .buttonStyle(.plain)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            if expanded {
                ForEach(calls) { call in
                    Text(call.text.split(separator: "\n").first.map(String.init) ?? call.title)
                        .font(.system(size: 11.5, design: .monospaced))
                        .lineLimit(1)
                        .truncationMode(.tail)
                        .foregroundStyle(DesktopPalette.text)
                        .textSelection(.enabled)
                }
            }
        }
        .padding(.leading, 12)
        .overlay(alignment: .leading) { Rectangle().fill(DesktopPalette.line).frame(width: 2) }
    }
}

struct ComposerView: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState
    @AppStorage("followUpDefault") private var followUpDefault = FollowUpAction.queue.rawValue
    @FocusState private var focused: Bool

    private var followUp: FollowUpAction { FollowUpAction(rawValue: followUpDefault) ?? .queue }
    private var hasText: Bool { !model.composer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    private var canSteer: Bool { session.capabilities.steer }

    var body: some View {
        VStack(spacing: 0) {
            if session.isReadOnly || !session.capabilities.send {
                Text(session.readOnlyReason ?? "This session is closed. You can read it, but not message it.")
                    .font(.system(size: 13))
                    .foregroundStyle(DesktopPalette.text2)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 15)
                    .padding(.vertical, 13)
                    .background(DesktopPalette.window, in: RoundedRectangle(cornerRadius: 16))
                    .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(DesktopPalette.line, style: StrokeStyle(lineWidth: 1, dash: [4])))
            } else {
                ForEach(model.deliveryProblems.filter { $0.sessionId == session.id }) { problem in
                    DeliveryProblemBanner(problem: problem, model: model)
                        .padding(.bottom, 8)
                }
                let queued = model.queuedItems(for: session.id)
                if !queued.isEmpty {
                    QueueDock(model: model, items: queued, canSteer: canSteer && session.isWorking)
                        .padding(.horizontal, 10)
                }
                VStack(spacing: 0) {
                    TextField("Message \(session.title)", text: $model.composer, axis: .vertical)
                        .textFieldStyle(.plain)
                        .font(.system(size: 14))
                        .lineLimit(2...10)
                        .focused($focused)
                        .padding(.horizontal, 15)
                        .padding(.top, 13)
                        .padding(.bottom, 4)
                        .onSubmit { submit(alternate: false) }
                    HStack(spacing: 4) {
                        Spacer()
                        if session.isWorking && hasText {
                            Text(hint)
                                .font(.system(size: 11))
                                .foregroundStyle(DesktopPalette.text2)
                                .padding(.trailing, 4)
                        }
                        Button(action: primaryAction) {
                            Image(systemName: primarySymbol)
                                .font(.system(size: primaryIsStop ? 10 : 14, weight: .bold))
                                .foregroundStyle(DesktopPalette.onAccent)
                                .frame(width: 32, height: 32)
                                .background(primaryIsStop ? DesktopPalette.stop : DesktopPalette.accent, in: Circle())
                        }
                        .buttonStyle(.plain)
                        .disabled(!session.isWorking && !hasText)
                        .opacity(!session.isWorking && !hasText ? 0.45 : 1)
                        .accessibilityLabel(primaryLabel)
                        .help(primaryHelp)
                        .keyboardShortcut(.return, modifiers: .command)
                        Button("") { submit(alternate: true) }
                            .keyboardShortcut(.return, modifiers: [.command, .option])
                            .hidden()
                            .frame(width: 0, height: 0)
                        Button("") { model.interruptSelected() }
                            .keyboardShortcut(".", modifiers: .command)
                            .hidden()
                            .frame(width: 0, height: 0)
                    }
                    .padding(.horizontal, 8)
                    .padding(.top, 6)
                    .padding(.bottom, 8)
                }
                .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(DesktopPalette.line))
                .shadow(color: .black.opacity(0.08), radius: 12, y: 6)
            }
            if let error = model.operationError {
                Text(error)
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.red)
                    .padding(.top, 6)
            }
        }
        .frame(maxWidth: 700)
        .padding(.horizontal, 24)
        .padding(.top, 40)
        .padding(.bottom, 20)
        .frame(maxWidth: .infinity)
        .background(
            LinearGradient(colors: [DesktopPalette.window.opacity(0), DesktopPalette.window], startPoint: .top, endPoint: UnitPoint(x: 0.5, y: 0.38))
                .allowsHitTesting(false)
        )
        .onAppear { focused = true }
    }

    private var primaryIsStop: Bool { session.isWorking && !hasText }
    private var primarySymbol: String {
        if primaryIsStop { return "stop.fill" }
        if session.isWorking && resolvedAction(alternate: false) == .queue { return "text.line.last.and.arrowtriangle.forward" }
        return "arrow.up"
    }
    private var primaryLabel: String {
        if primaryIsStop { return "Stop" }
        if !session.isWorking { return "Send" }
        return resolvedAction(alternate: false) == .queue ? "Queue" : "Steer"
    }
    private var primaryHelp: String {
        if primaryIsStop { return "Stop ⌘." }
        if !session.isWorking { return "Send ⌘↩" }
        return resolvedAction(alternate: false) == .queue ? "Queue ⌘↩ · Steer ⌥⌘↩" : "Steer ⌘↩ · Queue ⌥⌘↩"
    }
    private var hint: String {
        resolvedAction(alternate: false) == .queue ? "Sends after this turn · ⌥⌘↩ steers now" : "Steers now · ⌥⌘↩ queues"
    }

    private func resolvedAction(alternate: Bool) -> FollowUpAction {
        guard canSteer else { return .queue }
        let preferred = followUp
        return alternate ? (preferred == .queue ? .steer : .queue) : preferred
    }

    private func primaryAction() {
        if primaryIsStop {
            model.interruptSelected()
        } else {
            submit(alternate: false)
        }
    }

    private func submit(alternate: Bool) {
        guard hasText else { return }
        let steer = session.isWorking && resolvedAction(alternate: alternate) == .steer
        model.submit(model.composer, steer: steer)
        model.composer = ""
    }
}

struct DeliveryProblemBanner: View {
    let problem: DeliveryProblem
    @ObservedObject var model: AgenthailModel

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.triangle")
                .foregroundStyle(DesktopPalette.amber)
            VStack(alignment: .leading, spacing: 3) {
                Text("Not delivered · \(problem.reasonText)")
                    .font(.system(size: 12.5, weight: .medium))
                Text(PeerEnvelope(problem.message).body)
                    .lineLimit(2)
                    .foregroundStyle(DesktopPalette.text2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Button("Edit and resend") { model.resendDeliveryProblem(problem) }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.accentText)
            Button {
                model.dismissDeliveryProblem(problem)
            } label: {
                Image(systemName: "xmark")
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .accessibilityLabel("Dismiss")
        }
        .font(.system(size: 12.5))
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(DesktopPalette.warnBackground, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(DesktopPalette.warnLine))
    }
}

struct QueueDock: View {
    @ObservedObject var model: AgenthailModel
    let items: [QueueState]
    let canSteer: Bool

    var body: some View {
        VStack(spacing: 0) {
            ForEach(items) { item in
                let envelope = PeerEnvelope(item.message)
                HStack(spacing: 10) {
                    Image(systemName: "text.line.last.and.arrowtriangle.forward")
                        .font(.system(size: 11))
                        .foregroundStyle(DesktopPalette.text2)
                    HStack(spacing: 6) {
                        if let sender = envelope.sender {
                            Text(sender)
                                .foregroundStyle(DesktopPalette.text2)
                        }
                        Text(envelope.body.split(separator: "\n").first.map(String.init) ?? envelope.body)
                            .foregroundStyle(DesktopPalette.text)
                    }
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    if canSteer {
                        Button {
                            model.steerQueued(item)
                        } label: {
                            Label("Steer", systemImage: "arrow.turn.down.right")
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.text2)
                        .help("Steer into the current turn")
                    }
                    Button {
                        model.removeQueued(item)
                    } label: {
                        Image(systemName: "trash")
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityLabel("Remove queued message")
                    Menu {
                        if canSteer {
                            Button("Steer into current turn") { model.steerQueued(item) }
                        }
                        Button("Send after this turn (current)") {}
                            .disabled(true)
                        Button("Interrupt and send now") { model.interruptSelected() }
                        Divider()
                        Button("Edit") { model.removeQueued(item, restoreToComposer: true) }
                        Button("Copy text") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(item.message, forType: .string)
                        }
                        Divider()
                        Button("Remove", role: .destructive) { model.removeQueued(item) }
                    } label: {
                        Image(systemName: "ellipsis")
                    }
                    .menuStyle(.borderlessButton)
                    .menuIndicator(.hidden)
                    .fixedSize()
                    .accessibilityLabel("More actions")
                }
                .font(.system(size: 13))
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
            }
        }
        .padding(.top, 4)
        .padding(.bottom, 4)
        .background(DesktopPalette.dock, in: UnevenRoundedRectangle(topLeadingRadius: 14, topTrailingRadius: 14))
        .overlay(UnevenRoundedRectangle(topLeadingRadius: 14, topTrailingRadius: 14).strokeBorder(DesktopPalette.line))
    }
}

enum FollowUpAction: String, CaseIterable, Identifiable {
    case queue
    case steer

    var id: String { rawValue }
}

enum InspectorTab: String, CaseIterable, Identifiable {
    case details = "Details"
    case delivery = "Delivery"

    var id: String { rawValue }
}

struct SessionInspector: View {
    @ObservedObject var model: AgenthailModel
    @State private var tab: InspectorTab = .details

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 16) {
                ForEach(InspectorTab.allCases) { candidate in
                    Button(candidate.rawValue) { tab = candidate }
                        .buttonStyle(.plain)
                        .fontWeight(tab == candidate ? .semibold : .regular)
                        .foregroundStyle(tab == candidate ? DesktopPalette.text : DesktopPalette.text2)
                        .accessibilityAddTraits(tab == candidate ? .isSelected : [])
                }
                Spacer()
            }
            .font(.system(size: 12.5))
            .padding(.horizontal, 16)
            .frame(height: 52)
            .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
            if let session = model.selectedSession {
                ScrollView {
                    Group {
                        switch tab {
                        case .details: DetailsTab(model: model, session: session)
                        case .delivery: DeliveryTab(model: model, session: session)
                        }
                    }
                    .font(.system(size: 12.5))
                    .padding(16)
                    .frame(maxWidth: .infinity, alignment: .leading)
                }
            } else {
                Spacer()
            }
        }
        .background(DesktopPalette.side)
    }
}

struct DetailsTab: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let context = model.detail?.context, context.usedTokens > 0 {
                let ratio = context.contextWindow > 0 ? Double(context.usedTokens) / Double(context.contextWindow) : 0
                let knownWindow = context.contextWindow > 0 && !(context.windowEstimated == true && ratio > 1)
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text("Context").foregroundStyle(DesktopPalette.text2)
                        Spacer()
                        Text(knownWindow ? ratio.formatted(.percent.precision(.fractionLength(0))) : "\(context.usedTokens.formatted(.number.notation(.compactName))) tokens used")
                    }
                    if knownWindow {
                        ProgressView(value: min(ratio, 1))
                            .tint(DesktopPalette.accent)
                    }
                }
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 10, verticalSpacing: 9) {
                detailRow("Agent", session.alias.map { "@\($0)" } ?? session.name)
                detailRow("Surface", session.surface.capitalized)
                if let modelName = model.detail?.model { detailRow("Model", modelName) }
                if let project = session.hostProject?.displayName { detailRow("Project", project) }
                if let branch = session.checkout?.branch ?? session.checkout?.detachedHead { detailRow("Branch", branch, monospaced: true) }
                if let path = session.checkout?.path ?? session.cwd { detailRow("Checkout", path, monospaced: true) }
            }
            if let goal = model.detail?.goal, !goal.objective.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    SidebarCaption("Goal").padding(.horizontal, -8)
                    Text(goal.objective)
                    Text(goal.status.capitalized).foregroundStyle(DesktopPalette.text2)
                }
            }
        }
    }

    @ViewBuilder
    private func detailRow(_ label: String, _ value: String, monospaced: Bool = false) -> some View {
        GridRow {
            Text(label).foregroundStyle(DesktopPalette.text2)
            Text(value)
                .font(monospaced ? .system(size: 11.5, design: .monospaced) : .system(size: 12.5))
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

struct DeliveryTab: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState

    var body: some View {
        let queued = model.snapshot?.queue.filter { $0.sessionId == session.id && $0.status != "delivered" } ?? []
        let history = model.snapshot?.history.filter { $0.sessionId == session.id || $0.sourceSessionId == session.id } ?? []
        let problems = model.deliveryProblems(for: session.id)
        VStack(alignment: .leading, spacing: 8) {
            if !problems.isEmpty {
                SidebarCaption("Not delivered").padding(.horizontal, -8)
                ForEach(problems) { problem in
                    deliveryCard(title: problem.sessionId == session.id ? "To this session" : "To \(model.snapshot?.sessions.first { $0.id == problem.sessionId }?.title ?? "another session")", status: problem.reasonText, text: PeerEnvelope(problem.message).body, problem: true)
                }
            }
            if !queued.isEmpty {
                SidebarCaption("Queued").padding(.horizontal, -8)
                ForEach(queued) { item in
                    deliveryCard(title: item.target, status: "Queued", text: item.message, problem: false)
                }
            }
            if history.isEmpty && queued.isEmpty && problems.isEmpty {
                Text("No deliveries for this session yet.")
                    .foregroundStyle(DesktopPalette.text2)
            } else if !history.isEmpty {
                SidebarCaption("Recent").padding(.horizontal, -8)
                ForEach(history) { entry in
                    deliveryCard(title: entry.sourceSessionId == session.id ? "To \(entry.target ?? "session")" : "From \(entry.source ?? "you")", status: entry.error == nil ? (entry.result ?? entry.kind).capitalized : "Not delivered", text: entry.message ?? "", problem: entry.error != nil)
                }
            }
        }
    }

    private func deliveryCard(title: String, status: String, text: String, problem: Bool) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text(title)
                Spacer()
                Text(status)
                    .font(.system(size: 11.5))
                    .foregroundStyle(problem ? DesktopPalette.amber : DesktopPalette.text2)
            }
            Text(text)
                .lineLimit(1)
                .foregroundStyle(DesktopPalette.text2)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 9)
        .background(problem ? DesktopPalette.warnBackground : DesktopPalette.window, in: RoundedRectangle(cornerRadius: 8))
        .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(problem ? DesktopPalette.warnLine : DesktopPalette.line2))
    }
}

func relativeAge(_ timestamp: String?) -> String {
    guard let timestamp, let date = SessionTree.parseTimestamp(timestamp) else { return "" }
    let seconds = max(0, Date().timeIntervalSince(date))
    switch seconds {
    case ..<60: return "now"
    case ..<3600: return "\(Int(seconds / 60))m"
    case ..<86400: return "\(Int(seconds / 3600))h"
    default: return "\(Int(seconds / 86400))d"
    }
}
