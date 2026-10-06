import AppKit
import SwiftUI
import Textual

struct DesktopWindow: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane

    init(model: AgenthailModel) {
        self.model = model
        self.pane = model.mainPane
    }

    var body: some View {
        NavigationSplitView {
            SessionSidebar(model: model, pane: pane)
                .navigationSplitViewColumnWidth(min: 248, ideal: 264, max: 280)
        } detail: {
            ConversationPane(model: model, pane: pane)
                .opacity(model.isConnected || model.snapshot == nil ? 1 : 0.6)
                .ignoresSafeArea(.container, edges: .top)
                .inspector(isPresented: $pane.inspectorVisible) {
                    SessionInspector(model: model, pane: pane)
                        .ignoresSafeArea(.container, edges: .top)
                        .inspectorColumnWidth(min: 270, ideal: 284, max: 300)
                }
        }
        .disabled(model.paletteVisible)
        .background(DesktopPalette.window)
        .toolbar(removing: .sidebarToggle)
        .environmentObject(model)
        .sheet(item: $pane.renamingSession) { RenameSessionSheet(model: model, session: $0) }
        .sheet(isPresented: $model.newSessionVisible, onDismiss: {
            if model.newSessionMessage != nil { model.newSessionVisible = true }
        }) {
            NewSessionSheet(model: model)
        }
        .overlay(alignment: .top) {
            if model.paletteVisible {
                ZStack(alignment: .top) {
                    Color.black.opacity(0.18)
                        .ignoresSafeArea()
                        .onTapGesture { model.paletteVisible = false }
                    CommandPalette(model: model, pane: pane)
                        .padding(.top, 72)
                }
            }
        }
        .focusedSceneObject(pane)
        .onAppear { model.windowAppeared() }
        .onDisappear { model.windowDisappeared() }
    }
}

struct SessionSidebar: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @Environment(\.openWindow) private var openWindow
    @State private var expandedProjects: Set<String> = []
    @State private var expandedFamilies: Set<String> = []
    @FocusState private var searchFocused: Bool
    @ObservedObject private var shortcuts = ShortcutStore.shared

    var body: some View {
        let tree = model.sessionTree
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                ForEach(SessionFilter.allCases) { filter in
                    Button {
                        model.sessionFilter = filter
                    } label: {
                        HStack(spacing: 4) {
                            Text(filter.rawValue)
                                .fontWeight(model.sessionFilter == filter ? .semibold : .regular)
                                .foregroundStyle(model.sessionFilter == filter ? DesktopPalette.text : DesktopPalette.text2)
                            Text("\(tree.counts[filter] ?? 0)")
                                .font(.system(size: 11))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                        .lineLimit(1)
                        .fixedSize()
                    }
                    .buttonStyle(.plain)
                    .accessibilityAddTraits(model.sessionFilter == filter ? .isSelected : [])
                    .help(shortcuts.help(filter.rawValue, filter.command))
                }
                Spacer()
                SessionRefinementMenu(model: model)
                Button {
                    model.newSessionVisible = true
                } label: {
                    Image(systemName: "square.and.pencil")
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .help(shortcuts.help("New session", .newSession))
                .accessibilityLabel("New session")
            }
            .font(.system(size: 12.5))
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 8)

            HStack(spacing: 6) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.text2)
                TextField("Search sessions", text: Binding(get: { model.searchQuery }, set: { model.search($0) }))
                    .textFieldStyle(.plain)
                    .font(.system(size: 12.5))
                    .focused($searchFocused)
                    .onKeyPress(.escape) {
                        model.search("")
                        searchFocused = false
                        return .handled
                    }
                if !model.searchQuery.isEmpty {
                    Button {
                        model.search("")
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.muted)
                    .accessibilityLabel("Clear search")
                }
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 6)
            .background(DesktopPalette.selection.opacity(0.6), in: RoundedRectangle(cornerRadius: 7))
            .padding(.horizontal, 12)
            .padding(.bottom, 10)
            .background {
                Button("") { searchFocused = true }
                    .keyboardShortcut(shortcuts.keyboardShortcut(.findSession))
                    .hidden()
            }
            SessionRefinementBar(model: model)

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    if !model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        searchResults
                    } else {
                    if !tree.needsYou.isEmpty {
                        VStack(alignment: .leading, spacing: 2) {
                            SidebarCaption("Needs you")
                            ForEach(tree.needsYou) { session in
                                sessionButton(session, title: SessionFamilies.title(session, in: model.knownSessions), needsYou: true)
                            }
                        }
                    }
                    ForEach(tree.projects) { project in
                        let expanded = expandedProjects.contains(project.id)
                        let shown = expanded ? project : project.limited(to: SessionTree.collapsedSessionLimit, keeping: pane.selectedSessionID)
                        let single = project.checkouts.count == 1 ? project.checkouts.first : nil
                        VStack(alignment: .leading, spacing: 1) {
                            ProjectHeaderView(name: project.name, branch: single?.branchLabel, dirty: single?.dirty ?? false)
                            ForEach(shown.checkouts) { checkout in
                                if single == nil {
                                    CheckoutRowView(checkout: checkout)
                                }
                                ForEach(checkout.families) { family in
                                    familyRows(family)
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
                        Text(model.sessionRefinement.isActive ? "No sessions match these filters." : model.sessionFilter == .running ? "Nothing is running." : "No sessions yet.")
                            .font(.system(size: 12.5))
                            .foregroundStyle(DesktopPalette.text2)
                            .padding(.horizontal, 8)
                    }
                    }
                }
                .padding(.horizontal, 8)
                .padding(.bottom, 16)
            }

            ConnectionFooter(model: model)
        }
        .background(DesktopPalette.side)
        .background {
            let order = visibleOrder(tree)
            Button("") { step(order, by: -1) }
                .keyboardShortcut(shortcuts.keyboardShortcut(.previousSession))
                .hidden()
            Button("") { step(order, by: 1) }
                .keyboardShortcut(shortcuts.keyboardShortcut(.nextSession))
                .hidden()
        }
    }
}

extension SessionSidebar {
    @ViewBuilder
    private var searchResults: some View {
        let query = model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        let (local, remote) = searchMatches
        VStack(alignment: .leading, spacing: 2) {
            SidebarCaption("Sessions")
            ForEach(local) { session in
                sessionButton(session, title: SessionFamilies.title(session, in: model.knownSessions), needsYou: model.attentionSessionIDs.contains(session.id))
            }
            if local.isEmpty {
                Text("No matching sessions.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 4)
            }
        }
        if query.count >= 3 {
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    SidebarCaption("Codex history")
                    if model.searching { ProgressView().controlSize(.mini) }
                }
                ForEach(remote) { result in
                    Button {
                        model.openSearchResult(result.session)
                    } label: {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(result.session.title)
                                .lineLimit(1)
                                .foregroundStyle(DesktopPalette.text)
                            if let snippet = result.snippet, !snippet.isEmpty {
                                Text(snippet)
                                    .font(.system(size: 11.5))
                                    .lineLimit(2)
                                    .foregroundStyle(DesktopPalette.text2)
                            }
                        }
                        .font(.system(size: 13))
                        .padding(.vertical, 6)
                        .padding(.horizontal, 14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(pane.selectedSessionID == result.session.id ? DesktopPalette.selection : Color.clear, in: RoundedRectangle(cornerRadius: 7))
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
                if let error = model.searchError {
                    Text(error)
                        .font(.system(size: 12))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, 14)
                } else if !model.searching && remote.isEmpty {
                    Text("No older matches.")
                        .font(.system(size: 12))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 4)
                }
            }
        }
    }

    private var searchMatches: (local: [SessionState], remote: [SessionSearchItem]) {
        let query = model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        let local = SessionTree.newestFirst(model.knownSessions.filter { session in
            [session.title, session.name, session.hostProject?.displayName, session.checkout?.branch]
                .compactMap { $0 }
                .contains { $0.localizedCaseInsensitiveContains(query) }
        })
        let localIDs = Set(local.map(\.id))
        let remote = query.count >= 3 ? model.searchResults.filter { !localIDs.contains($0.session.id) } : []
        return (local, remote)
    }

    private func visibleOrder(_ tree: SessionTree) -> [String] {
        if !model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            let matches = searchMatches
            return matches.local.map(\.id) + matches.remote.map(\.session.id)
        }
        var ids = tree.needsYou.map(\.id)
        for project in tree.projects {
            let shown = expandedProjects.contains(project.id) ? project : project.limited(to: SessionTree.collapsedSessionLimit, keeping: pane.selectedSessionID)
            for family in shown.checkouts.flatMap(\.families) {
                let rows = [family.root.id] + (familyExpanded(family) ? family.members.map(\.id) : [])
                ids += rows.filter { !ids.contains($0) }
            }
        }
        return ids
    }

    private func familyExpanded(_ family: SessionFamily) -> Bool {
        !family.members.isEmpty && (expandedFamilies.contains(family.id) || family.members.contains { $0.id == pane.selectedSessionID })
    }

    @ViewBuilder
    private func familyRows(_ family: SessionFamily) -> some View {
        let expanded = familyExpanded(family)
        HStack(spacing: 0) {
            sessionButton(family.root, title: family.root.title, family: family, needsYou: model.attentionSessionIDs.contains(family.root.id))
            if !family.members.isEmpty {
                Button {
                    if expandedFamilies.contains(family.id) { expandedFamilies.remove(family.id) } else { expandedFamilies.insert(family.id) }
                } label: {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 10, weight: .semibold))
                        .frame(width: 18, height: 22)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .accessibilityLabel(expanded ? "Hide subagents" : "Show subagents")
            }
        }
        if expanded {
            ForEach(family.members) { member in
                sessionButton(member.session, title: SessionFamilies.label(member.session), needsYou: model.attentionSessionIDs.contains(member.id))
                    .padding(.leading, CGFloat(member.depth) * 16)
            }
        }
    }

    private func step(_ order: [String], by offset: Int) {
        guard !order.isEmpty else { return }
        let current = pane.selectedSessionID.flatMap { order.firstIndex(of: $0) }
        let next = current.map { min(max($0 + offset, 0), order.count - 1) } ?? 0
        if let result = model.searchResults.first(where: { $0.session.id == order[next] }) {
            model.openSearchResult(result.session)
        } else {
            pane.select(order[next])
        }
    }

    private func sessionButton(_ session: SessionState, title: String, family: SessionFamily? = nil, needsYou: Bool) -> some View {
        let selected = pane.selectedSessionID == session.id
        return Button {
            pane.select(session.id)
        } label: {
            SessionRowView(session: session, title: title, family: family, needsYou: needsYou, finishedUnseen: model.finishedUnseen.contains(session.id))
                .padding(.vertical, 6)
                .padding(.horizontal, 8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(selected ? DesktopPalette.selection : (needsYou ? DesktopPalette.warnBackground : Color.clear), in: RoundedRectangle(cornerRadius: 7))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .contextMenu {
            Button("Open in New Window") { openWindow(id: "session", value: session.id) }
            Button("Rename…") { pane.renamingSession = session }
        }
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

struct ProjectHeaderView: View {
    let name: String
    var branch: String? = nil
    var dirty = false

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
                .lineLimit(1)
            if let branch {
                Text(branch)
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            if dirty {
                Circle().fill(DesktopPalette.amber).frame(width: 5, height: 5)
                    .accessibilityLabel("Uncommitted changes")
            }
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
    var title: String? = nil
    var family: SessionFamily? = nil
    let needsYou: Bool
    var finishedUnseen = false

    var body: some View {
        HStack(spacing: 9) {
            StatusIndicator(session: session, needsYou: needsYou, finishedUnseen: finishedUnseen)
            Text(title ?? session.title)
                .lineLimit(1)
                .foregroundStyle(session.open || session.isWorking ? DesktopPalette.text : DesktopPalette.text2)
            Spacer(minLength: 4)
            if let family, family.subagentCount > 0 {
                HStack(spacing: 3) {
                    Image(systemName: "person.2")
                    Text(family.workingSubagents > 0 ? "\(family.workingSubagents)/\(family.subagentCount)" : "\(family.subagentCount)")
                }
                .font(.system(size: 10.5))
                .foregroundStyle(family.workingSubagents > 0 ? DesktopPalette.work : DesktopPalette.text2)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(SessionFamilies.subagentSummary(family))
            }
            if let shared = SharedConversation.badge(session) {
                Text(shared)
                    .font(.system(size: 10.5, design: .monospaced))
                    .foregroundStyle(DesktopPalette.accentText)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(SharedConversation.badgeLabel(session))
                    .help(SharedConversation.badgeLabel(session))
            }
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
    var finishedUnseen = false

    var body: some View {
        Group {
            if session.isWorking && !needsYou {
                ProgressView()
                    .controlSize(.mini)
                    .tint(DesktopPalette.work)
                    .scaleEffect(0.8)
            } else {
                Circle()
                    .fill(DesktopPalette.statusColor(session, needsYou: needsYou, finishedUnseen: finishedUnseen))
                    .frame(width: 7, height: 7)
            }
        }
        .frame(width: 12, height: 12)
        .accessibilityHidden(true)
    }
}

struct ConnectionFooter: View {
    @ObservedObject var model: AgenthailModel

    private var offlineLabel: String {
        if model.daemonSlow { return "Agenthail is slow to respond" }
        if model.loading && model.connectionError == nil { return "Loading sessions…" }
        guard model.snapshot != nil, let loadedAt = model.snapshotLoadedAt else { return "Agenthail isn't running" }
        return "Offline · as of \(relativeAge(loadedAt.formatted(.iso8601)))"
    }

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(model.isConnected ? DesktopPalette.green : DesktopPalette.amber)
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)
            Text(model.isConnected ? "Connected · this Mac" : model.reconnecting ? "Reconnecting…" : offlineLabel)
            Spacer()
            if !model.isConnected && !model.reconnecting && !model.daemonSlow {
                Button("Start") { model.restartDaemon() }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accentText)
                    .help("Start the Agenthail daemon")
            }
            SettingsLink {
                Image(systemName: "slider.horizontal.3")
            }
            .buttonStyle(.plain)
            .help("Settings ⌘,")
            .accessibilityLabel("Settings")
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
    @ObservedObject var pane: SessionPane
    var headerInset: CGFloat = 22

    var body: some View {
        if let session = pane.displayedSession {
            VStack(spacing: 0) {
                ConversationHeader(session: session, title: SessionFamilies.title(session, in: model.knownSessions), model: pane.detail?.model, context: pane.detail?.context, inspectorVisible: $pane.inspectorVisible, leadingInset: headerInset, onFocusTerminal: { model.focusInTerminal(session) })
                if let note = SharedConversation.note(session) {
                    SharedConversationBanner(note: note, peers: SharedConversation.peers(session), leadingInset: headerInset, onOpen: pane.select)
                }
                TranscriptView(model: model, pane: pane, session: session)
                    .safeAreaInset(edge: .bottom, spacing: 0) {
                        if pane.removedSession?.id == session.id {
                            RemovedSessionBar(onClose: pane.closeRemovedSession)
                        } else {
                            ComposerView(model: model, pane: pane, session: session)
                        }
                    }
            }
            .background(DesktopPalette.window)
            .environmentObject(pane)
        } else {
            ContentUnavailableView {
                if model.isConnected {
                    Label("Select a session", systemImage: "bubble.left.and.bubble.right")
                } else if model.daemonSlow {
                    Label("Waiting for Agenthail", systemImage: "hourglass")
                } else {
                    Label("Agenthail isn't running", systemImage: "bolt.horizontal.circle")
                }
            } description: {
                if model.isConnected {
                    Text("Choose a session from the sidebar.")
                } else if model.daemonSlow {
                    Text("Agenthail is running but took too long to answer. Retrying.")
                } else {
                    Text(model.connectionError ?? "The app can't reach the Agenthail daemon.")
                }
            } actions: {
                if !model.isConnected && !model.daemonSlow {
                    Button("Start Agenthail") { model.restartDaemon() }
                }
            }
            .background(DesktopPalette.window)
        }
    }
}

struct RemovedSessionBar: View {
    let onClose: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "archivebox")
                .foregroundStyle(DesktopPalette.text2)
            VStack(alignment: .leading, spacing: 2) {
                Text("This session is no longer available")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(DesktopPalette.text)
                Text("Agenthail stopped finding it. You're reading its last loaded content.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Spacer()
            Button("Close", action: onClose)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(DesktopPalette.dock, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(DesktopPalette.line))
        .frame(maxWidth: 760)
        .padding(.horizontal, 24)
        .padding(.bottom, 16)
    }
}

struct ConversationHeader: View {
    let session: SessionState
    let title: String
    let model: String?
    let context: ContextState?
    @Binding var inspectorVisible: Bool
    var leadingInset: CGFloat = 22
    @ObservedObject private var shortcuts = ShortcutStore.shared
    var onFocusTerminal: () -> Void = {}

    var body: some View {
        HStack(spacing: 12) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text(session.subagent != nil ? title : session.alias.map { _ in session.name.isEmpty ? session.title : session.name } ?? title)
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
            Text(([session.surface.capitalized, model] + [context?.headerLabel]).compactMap { $0 }.joined(separator: " · "))
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
            if session.runtime?.focusable == true, let host = session.runtime?.hostName {
                Button("Open in \(host)") { onFocusTerminal() }
                    .buttonStyle(.plain)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.accentText)
                    .help("Bring this session's terminal to the front")
            }
            Button {
                inspectorVisible.toggle()
            } label: {
                Image(systemName: "sidebar.right")
                    .frame(width: 30, height: 30)
                    .background(inspectorVisible ? DesktopPalette.selection : Color.clear, in: RoundedRectangle(cornerRadius: 7))
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .help(shortcuts.help("Inspector", .toggleInspector))
            .accessibilityLabel("Toggle inspector")
            .accessibilityValue(inspectorVisible ? "Shown" : "Hidden")
        }
        .padding(.leading, leadingInset)
        .padding(.trailing, 14)
        .frame(height: 52)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
    }
}

struct TranscriptView: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var pinned = true
    @State private var prepending = false
    @State private var underfilled = false
    @ObservedObject private var shortcuts = ShortcutStore.shared

    var body: some View {
        let blocks = TranscriptBlock.build(pane.timelineItems)
        let sends = model.localSends[session.id] ?? []
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if (pane.olderCursor ?? 0) > 0 || pane.olderError != nil {
                    olderControl
                }
                if let error = pane.detail?.readError {
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
        .scrollPosition($position)
        .defaultScrollAnchor(.bottom, for: .initialOffset)
        .defaultScrollAnchor(pinned || prepending ? .bottom : nil, for: .sizeChanges)
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentOffset.y + geometry.containerSize.height >= geometry.contentSize.height - 70
        } action: { _, atBottom in
            pinned = atBottom
        }
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentSize.height < geometry.containerSize.height * 1.5
        } action: { _, value in
            underfilled = value
            if value { fillViewport() }
        }
        .onChange(of: pane.olderItems.count) { if underfilled { fillViewport() } }
        .onChange(of: pane.detail?.session.id) { if underfilled { fillViewport() } }
        .onChange(of: session.id) {
            pinned = true
            position.scrollTo(edge: .bottom)
        }
        .overlay(alignment: .bottomTrailing) {
            if !pinned {
                Button {
                    jumpToLatest()
                } label: {
                    Label("Jump to latest", systemImage: "arrow.down")
                        .font(.system(size: 12))
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .background(DesktopPalette.raised, in: Capsule())
                        .overlay(Capsule().strokeBorder(DesktopPalette.line))
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .padding(16)
            }
        }
        .background {
            Button("", action: jumpToLatest)
                .keyboardShortcut(shortcuts.keyboardShortcut(.jumpToLatest))
                .hidden()
        }
        .overlay {
            if pane.detail == nil, pane.selectedSessionID == session.id {
                VStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    if let error = pane.detailLoadError {
                        Text("Couldn't load this session. Retrying… \(error)")
                            .font(.system(size: 12))
                            .foregroundStyle(DesktopPalette.text2)
                            .multilineTextAlignment(.center)
                            .frame(maxWidth: 360)
                    }
                }
            }
        }
        .overlay(alignment: .top) {
            if pane.detailStale {
                HStack(spacing: 6) {
                    if !pane.detailRefreshFailed {
                        ProgressView().controlSize(.mini)
                        Text("Updating…")
                    } else {
                        Image(systemName: "exclamationmark.triangle")
                        Text("Couldn't refresh · showing last loaded")
                    }
                }
                .font(.system(size: 11.5))
                .foregroundStyle(DesktopPalette.text2)
                .padding(.horizontal, 10)
                .padding(.vertical, 4)
                .background(DesktopPalette.raised, in: Capsule())
                .overlay(Capsule().stroke(DesktopPalette.line))
                .padding(.top, 8)
                .transition(.opacity)
            }
        }
    }

    private var olderControl: some View {
        HStack(spacing: 8) {
            if pane.loadingOlder {
                ProgressView().controlSize(.mini)
            } else if (pane.olderCursor ?? 0) > 0 {
                Button("Load earlier") {
                    prepending = true
                    Task {
                        await pane.loadOlder()
                        try? await Task.sleep(for: .milliseconds(300))
                        prepending = false
                    }
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.accentText)
            }
            if let error = pane.olderError {
                Text(error).foregroundStyle(DesktopPalette.text2)
            }
        }
        .font(.system(size: 12))
        .frame(maxWidth: .infinity)
    }

    private func fillViewport() {
        guard pane.detail != nil, let cursor = pane.olderCursor, cursor > 0, !pane.loadingOlder, pane.olderError == nil else { return }
        prepending = true
        Task {
            await pane.loadOlder()
            try? await Task.sleep(for: .milliseconds(300))
            prepending = false
        }
    }

    private func jumpToLatest() {
        pinned = true
        withAnimation(.easeOut(duration: 0.25)) { position.scrollTo(edge: .bottom) }
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
        case .peer(let sender, let text):
            UserBubble(text: text, receipt: nil, sender: sender)
        case .tools(let items):
            ToolRunView(items: items, expanded: $expanded)
        case .annotation(let text):
            Text(text)
                .font(.system(size: 11.5))
                .foregroundStyle(DesktopPalette.muted)
        case .notice(let text):
            Button { expanded.toggle() } label: {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Image(systemName: "info.circle")
                    Text(text)
                        .lineLimit(expanded ? nil : 1)
                        .multilineTextAlignment(.leading)
                }
                .font(.system(size: 11.5))
                .foregroundStyle(DesktopPalette.muted)
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(expanded ? "" : text)
        case .image(let attachment):
            AttachmentImageView(attachment: attachment)
        }
    }
}

struct AttachmentImageView: View {
    @EnvironmentObject private var model: AgenthailModel
    @EnvironmentObject private var pane: SessionPane
    let attachment: SessionAttachment
    @State private var image: NSImage?
    @State private var failed = false

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
                    .aspectRatio(contentMode: .fit)
                    .frame(maxWidth: 520, maxHeight: 360, alignment: .leading)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                    .overlay(RoundedRectangle(cornerRadius: 8).strokeBorder(DesktopPalette.line))
                    .onTapGesture(count: 2) { open(image) }
                    .contextMenu {
                        Button("Open in Preview") { open(image) }
                        Button("Copy image") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.writeObjects([image])
                        }
                    }
            } else if failed {
                Text("Image couldn't be loaded")
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.muted)
            } else {
                RoundedRectangle(cornerRadius: 8)
                    .fill(DesktopPalette.bubble)
                    .frame(width: 220, height: 140)
                    .overlay { ProgressView().controlSize(.small) }
            }
        }
        .task(id: attachment.id) { await load() }
    }

    private func load() async {
        guard let sessionID = pane.displayedSession?.id else { return }
        do {
            let data = try await model.attachmentData(sessionID: sessionID, attachment: attachment)
            if let decoded = NSImage(data: data) { image = decoded } else { failed = true }
        } catch {
            if !error.isCancellation { failed = true }
        }
    }

    private func open(_ image: NSImage) {
        guard let tiff = image.tiffRepresentation, let bitmap = NSBitmapImageRep(data: tiff), let png = bitmap.representation(using: .png, properties: [:]) else { return }
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("agenthail-\(attachment.id).png")
        guard (try? png.write(to: url)) != nil else { return }
        NSWorkspace.shared.open(url)
    }
}

struct UserBubble: View {
    let text: String
    let receipt: String?
    var sender: String? = nil
    @State private var expanded = false

    var body: some View {
        let envelope = sender.map { PeerEnvelope(sender: $0, body: text) } ?? PeerEnvelope(text)
        VStack(alignment: .trailing, spacing: 5) {
            if let sender = envelope.sender {
                Text("From \(sender)")
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Text(envelope.body)
                .font(.system(size: 14))
                .lineSpacing(3)
                .lineLimit(expanded ? nil : 8)
                .textSelection(.enabled)
                .padding(.horizontal, 13)
                .padding(.vertical, 9)
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: 14))
            if envelope.body.split(separator: "\n", omittingEmptySubsequences: false).count > 8 || envelope.body.count > 700 {
                Button(expanded ? "Show less" : "Show more") { expanded.toggle() }
                    .buttonStyle(.plain)
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.text2)
            }
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
        let entries = ToolRunSummary.entries(items)
        let failed = items.filter(ToolRunSummary.isFailure).count
        VStack(alignment: .leading, spacing: 6) {
            Button {
                expanded.toggle()
            } label: {
                HStack(spacing: 7) {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: 10, weight: .semibold))
                    Text(ToolRunSummary.label(items))
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
                ForEach(entries) { entry in
                    switch entry {
                    case .call(let call, let results):
                        ToolCallRow(call: call, results: results)
                    case .output(let result):
                        ToolOutputBlock(result: result)
                            .padding(.leading, 21)
                    case .reasoning(let item):
                        ReasoningRow(item: item)
                    }
                }
            }
        }
        .padding(.leading, 12)
        .overlay(alignment: .leading) { Rectangle().fill(DesktopPalette.line).frame(width: 2) }
    }

}

struct ToolCallRow: View {
    let call: TimelineItem
    let results: [TimelineItem]
    @State private var expanded = false

    private var failed: Bool { results.contains(where: ToolRunSummary.isFailure) || ToolRunSummary.isFailure(call) }

    static func fullInput(_ text: String) -> String {
        guard let object = try? JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any], !object.isEmpty else { return text }
        let command = (object["command"] ?? object["cmd"]) as? String
        let rest = object.filter { $0.key != "command" && $0.key != "cmd" }
        var lines: [String] = command.map { ["$ " + $0] } ?? []
        for key in rest.keys.sorted() {
            let value = rest[key].map { value -> String in
                if let string = value as? String { return string }
                if JSONSerialization.isValidJSONObject(value), let data = try? JSONSerialization.data(withJSONObject: value, options: [.prettyPrinted, .sortedKeys]) {
                    return String(decoding: data, as: UTF8.self)
                }
                return "\(value)"
            } ?? ""
            lines.append("\(key): \(value)")
        }
        return lines.joined(separator: "\n")
    }

    var body: some View {
        let presentation = ToolPresentation(name: call.title, text: call.text)
        let isCommand: Bool = { if case .command = presentation.content { return true } else { return false } }()
        let firstLine = presentation.summary.split(separator: "\n", omittingEmptySubsequences: true).first.map(String.init) ?? ""
        VStack(alignment: .leading, spacing: 4) {
            Button {
                expanded.toggle()
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: 7) {
                    Image(systemName: presentation.symbol)
                        .font(.system(size: 10.5))
                        .frame(width: 14)
                    Text(presentation.intent ?? presentation.title)
                        .lineLimit(1)
                        .layoutPriority(1)
                    Text(isCommand ? "$ " + firstLine : firstLine)
                        .font(.system(size: 11.5, design: .monospaced))
                        .foregroundStyle(presentation.intent == nil ? DesktopPalette.text : DesktopPalette.muted)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                .font(.system(size: 12))
                .foregroundStyle(failed ? DesktopPalette.red : DesktopPalette.text2)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            if expanded {
                VStack(alignment: .leading, spacing: 6) {
                    Text(Self.fullInput(call.text))
                        .font(.system(size: 11.5, design: .monospaced))
                        .foregroundStyle(DesktopPalette.text)
                        .textSelection(.enabled)
                        .lineLimit(40)
                        .padding(8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: 6))
                    if results.isEmpty {
                        Text("No result yet")
                            .font(.system(size: 11.5))
                            .foregroundStyle(DesktopPalette.muted)
                    }
                    ForEach(results) { result in
                        ToolOutputBlock(result: result)
                    }
                }
                .padding(.leading, 21)
            }
        }
        .contextMenu {
            Button("Copy input") { copyToPasteboard(call.text) }
            if !results.isEmpty {
                Button("Copy output") { copyToPasteboard(results.map(\.text).joined(separator: "\n\n")) }
            }
        }
    }
}

struct ReasoningRow: View {
    let item: TimelineItem
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button {
                expanded.toggle()
            } label: {
                HStack(alignment: .firstTextBaseline, spacing: 7) {
                    Image(systemName: "brain")
                        .font(.system(size: 10.5))
                        .frame(width: 14)
                    Text("Reasoning")
                    Text(item.text.split(separator: "\n", omittingEmptySubsequences: true).first.map(String.init) ?? "")
                        .foregroundStyle(DesktopPalette.muted)
                        .lineLimit(1)
                        .truncationMode(.tail)
                }
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.text2)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
            if expanded {
                StructuredText(markdown: item.text)
                    .font(.system(size: 13, design: .serif))
                    .foregroundStyle(DesktopPalette.text2)
                    .textSelection(.enabled)
                    .padding(.leading, 21)
            }
        }
    }
}

struct ToolOutputBlock: View {
    let result: TimelineItem
    @State private var showAll = false

    var body: some View {
        let failed = ToolRunSummary.isFailure(result)
        let text = result.text
        let preview = ToolRunSummary.outputPreview(text)
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 10) {
                Text(failed ? "Error output" : "Output")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(failed ? DesktopPalette.red : DesktopPalette.muted)
                Spacer()
                if preview.count < text.count {
                    Button(showAll ? "Show less output" : "Show full output") { showAll.toggle() }
                }
                Button("Copy output") { copyToPasteboard(text) }
            }
            .font(.system(size: 11))
            .buttonStyle(.link)
            Text(text.isEmpty ? "The tool returned an empty result." : showAll ? text : preview)
                .font(.system(size: 11.5, design: .monospaced))
                .foregroundStyle(failed ? DesktopPalette.red : DesktopPalette.text)
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: 6))
            if result.truncated {
                Label("Output shortened by the host", systemImage: "text.badge.ellipsis")
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.muted)
            }
        }
    }
}

private func copyToPasteboard(_ text: String) {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(text, forType: .string)
}

struct ComposerView: View {
    @ObservedObject var model: AgenthailModel
    let pane: SessionPane
    let session: SessionState
    @FocusState private var focused: Bool
    @State private var dropTargeted = false
    @ObservedObject private var shortcuts = ShortcutStore.shared

    private var followUp: FollowUpAction { model.busyDelivery }
    @ObservedObject private var draft: ComposerDraft

    init(model: AgenthailModel, pane: SessionPane, session: SessionState) {
        self.model = model
        self.pane = pane
        self.session = session
        self.draft = pane.composerDraft
    }

    private var hasText: Bool { !draft.isEmpty }
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
                let queued = model.queuedItems(for: session.id)
                BoundedDock {
                    VStack(spacing: 0) {
                        ForEach(model.deliveryProblems.filter { $0.sessionId == session.id }) { problem in
                            DeliveryProblemBanner(problem: problem, model: model)
                                .padding(.bottom, 8)
                        }
                        if !queued.isEmpty {
                            QueueDock(model: model, items: queued, canSteer: canSteer && session.isWorking)
                                .padding(.horizontal, 10)
                        }
                        if !draft.attachments.isEmpty {
                            AttachmentDock(attachments: $draft.attachments, roundedTop: queued.isEmpty)
                                .padding(.horizontal, 10)
                        }
                    }
                }
                VStack(spacing: 0) {
                    TextField("Message \(session.title)", text: $draft.text, axis: .vertical)
                        .textFieldStyle(.plain)
                        .font(.system(size: 14))
                        .lineLimit(2...10)
                        .focused($focused)
                        .padding(.horizontal, 15)
                        .padding(.top, 13)
                        .padding(.bottom, 4)
                        .onSubmit { submit(alternate: false) }
                    HStack(spacing: 4) {
                        Button(action: chooseAttachments) {
                            Image(systemName: "paperclip")
                                .font(.system(size: 13))
                                .frame(width: 28, height: 28)
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.text2)
                        .help("Attach files or images")
                        .accessibilityLabel("Attach files or images")
                        .padding(.leading, 5)
                        if session.surface == "codex" {
                            TurnSettingsMenu(model: model, sessionID: session.id, detail: pane.detail)
                        }
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
                        .keyboardShortcut(shortcuts.keyboardShortcut(.send))
                        Button("") { submit(alternate: true) }
                            .keyboardShortcut(shortcuts.keyboardShortcut(.sendAlternate))
                            .hidden()
                            .frame(width: 0, height: 0)
                    }
                    .padding(.horizontal, 8)
                    .padding(.top, 6)
                    .padding(.bottom, 8)
                }
                .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(dropTargeted ? DesktopPalette.accent : DesktopPalette.line, lineWidth: dropTargeted ? 2 : 1))
                .shadow(color: .black.opacity(0.08), radius: 12, y: 6)
                .dropDestination(for: URL.self) { urls, _ in
                    guard let files = ComposerDrop.files(urls) else { return false }
                    draft.attachments = ComposerDrop.adding(files, to: draft.attachments)
                    focused = true
                    return true
                } isTargeted: { dropTargeted = $0 }
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

    private var primaryIsStop: Bool { SessionPane.stopAvailable(session, removed: false, draftEmpty: draft.isEmpty) }

    private func chooseAttachments() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = true
        panel.prompt = "Attach"
        guard panel.runModal() == .OK else { return }
        draft.attachments = ComposerDrop.adding(panel.urls, to: draft.attachments)
        focused = true
    }
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
        if primaryIsStop { return shortcuts.help("Stop", .stop) }
        if !session.isWorking { return shortcuts.help("Send", .send) }
        guard canSteer else { return shortcuts.help("Queue", .send) }
        let queueFirst = resolvedAction(alternate: false) == .queue
        return [shortcuts.help(queueFirst ? "Queue" : "Steer", .send), shortcuts.help(queueFirst ? "Steer" : "Queue", .sendAlternate)].joined(separator: " · ")
    }
    private var hint: String {
        guard canSteer else { return "Sends after this turn" }
        let queueFirst = resolvedAction(alternate: false) == .queue
        guard let alternate = shortcuts.label(.sendAlternate) else { return queueFirst ? "Sends after this turn" : "Steers now" }
        return queueFirst ? "Sends after this turn · \(alternate) steers now" : "Steers now · \(alternate) queues"
    }

    private func resolvedAction(alternate: Bool) -> FollowUpAction {
        guard canSteer else { return .queue }
        let preferred = followUp
        return alternate ? (preferred == .queue ? .steer : .queue) : preferred
    }

    private func primaryAction() {
        if primaryIsStop {
            pane.interrupt()
        } else {
            submit(alternate: false)
        }
    }

    private func submit(alternate: Bool) {
        guard hasText else { return }
        let explicit = session.isWorking && alternate && canSteer ? resolvedAction(alternate: true).rawValue : nil
        pane.submit(busyDelivery: explicit)
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
                        Button("Interrupt and send now") { model.perform(action: "interrupt", sessionID: item.sessionId) }
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

struct AttachmentDock: View {
    @Binding var attachments: [URL]
    let roundedTop: Bool

    var body: some View {
        let shape = UnevenRoundedRectangle(topLeadingRadius: roundedTop ? 14 : 0, topTrailingRadius: roundedTop ? 14 : 0)
        VStack(spacing: 0) {
            ForEach(attachments, id: \.self) { url in
                HStack(spacing: 10) {
                    Image(systemName: ComposerDrop.isImage(url) ? "photo" : url.hasDirectoryPath ? "folder" : "doc")
                        .font(.system(size: 11))
                        .foregroundStyle(DesktopPalette.text2)
                        .frame(width: 14)
                    HStack(spacing: 6) {
                        Text(url.lastPathComponent)
                            .foregroundStyle(DesktopPalette.text)
                        Text(url.deletingLastPathComponent().path.replacingOccurrences(of: FileManager.default.homeDirectoryForCurrentUser.path, with: "~"))
                            .foregroundStyle(DesktopPalette.text2)
                            .truncationMode(.head)
                    }
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .help(url.path)
                    Button {
                        attachments.removeAll { $0 == url }
                    } label: {
                        Image(systemName: "xmark")
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityLabel("Remove \(url.lastPathComponent)")
                }
                .font(.system(size: 13))
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
            }
        }
        .padding(.vertical, 4)
        .background(DesktopPalette.dock, in: shape)
        .overlay(shape.strokeBorder(DesktopPalette.line))
    }
}

enum InspectorTab: String, CaseIterable, Identifiable {
    case details = "Details"
    case delivery = "Delivery"

    var id: String { rawValue }
}

struct SessionInspector: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
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
            if let session = pane.displayedSession {
                ScrollView {
                    Group {
                        switch tab {
                        case .details: DetailsTab(model: model, pane: pane, session: session)
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

extension ContextState {
    var headerLabel: String? {
        guard usedTokens > 0 else { return nil }
        if let ratio = fraction { return "\(ratio.formatted(.percent.precision(.fractionLength(0)))) context" }
        return "\(usedTokens.formatted(.number.notation(.compactName))) tokens"
    }
}

struct DetailsTab: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState
    @State private var showTokens = false
    private var modelOptions: [ModelOption] {
        if let options = pane.detail?.models, !options.isEmpty { return options }
        return model.modelCatalog[session.surface] ?? []
    }

    private var catalogNeeded: String? {
        guard session.capabilities.model, pane.modelCatalogNeeded, pane.detail?.models?.isEmpty ?? true else { return nil }
        return session.surface
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let context = pane.detail?.context {
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text("Context").foregroundStyle(DesktopPalette.text2)
                        Spacer()
                        Text(ContextBreakdown.usage(context))
                    }
                    if let ratio = context.fraction {
                        ProgressView(value: min(ratio, 1))
                            .tint(DesktopPalette.accent)
                    }
                    if context.compacting {
                        Text("Compacting context")
                            .foregroundStyle(DesktopPalette.muted)
                    }
                    DisclosureGroup("Token details", isExpanded: $showTokens) {
                        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 10, verticalSpacing: 6) {
                            ForEach(ContextBreakdown.rows(context), id: \.label) { row in
                                GridRow {
                                    Text(row.label).foregroundStyle(DesktopPalette.text2)
                                    Text(row.value)
                                        .monospacedDigit()
                                        .textSelection(.enabled)
                                        .gridColumnAlignment(.trailing)
                                }
                            }
                        }
                        .padding(.top, 4)
                    }
                    .foregroundStyle(DesktopPalette.text2)
                }
            }
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 10, verticalSpacing: 9) {
                GridRow {
                    Text("Agent").foregroundStyle(DesktopPalette.text2)
                    HStack(spacing: 6) {
                        Text(session.alias.map { "@\($0)" } ?? session.name)
                            .textSelection(.enabled)
                        Button("Rename") { pane.renamingSession = session }
                            .buttonStyle(.plain)
                            .foregroundStyle(DesktopPalette.accentText)
                    }
                }
                detailRow("Surface", session.surface.capitalized)
                let modelName = pane.detail?.model
                if canControl && session.capabilities.model, case let options = modelOptions, !options.isEmpty {
                    GridRow {
                        Text("Model").foregroundStyle(DesktopPalette.text2)
                        Menu(options.first { $0.id == modelName }?.displayName ?? modelName ?? "Choose") {
                            ForEach(options) { option in
                                Button {
                                    pane.changeModel(to: option.id)
                                } label: {
                                    if option.id == modelName { Label(option.displayName, systemImage: "checkmark") } else { Text(option.displayName) }
                                }
                            }
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                        .disabled(pane.controlPending)
                    }
                } else if let modelName {
                    detailRow("Model", modelName)
                }
                if let project = session.hostProject?.displayName { detailRow("Project", project) }
                if let branch = session.checkout?.branch ?? session.checkout?.detachedHead { detailRow("Branch", branch, monospaced: true) }
                if let path = session.checkout?.path ?? session.cwd { detailRow("Checkout", (path as NSString).abbreviatingWithTildeInPath, monospaced: true) }
            }
            if canControl && session.capabilities.compact {
                Button("Compact context") { pane.compactContext() }
                    .disabled(pane.controlPending)
                    .help("Ask the agent to compact its context")
            }
            GoalSection(model: model, pane: pane, session: session)
            SubagentsSection(model: model, pane: pane, session: session)
            ClaudeObservationsSection(detail: pane.detail)
        }
        .task(id: catalogNeeded) {
            if let surface = catalogNeeded { await model.loadModelCatalog(surface: surface) }
        }
    }

    private var canControl: Bool { !session.isReadOnly && pane.removedSession == nil }

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
