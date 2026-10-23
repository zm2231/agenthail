import AppKit
import SwiftUI
import Textual
import UniformTypeIdentifiers

struct DesktopWindow: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    private let snapshot: Bool

    /// A snapshot pane draws an offscreen copy that leaves the person's window, focus and saved layout alone.
    init(model: AgenthailModel, snapshotPane: SessionPane? = nil) {
        self.model = model
        self.pane = snapshotPane ?? model.mainPane
        self.snapshot = snapshotPane != nil
    }

    var body: some View {
        WindowChromeLayout(model: model, pane: pane, interface: model.interface) {
            splitView
        }
        .disabled(model.paletteVisible)
        .windowBackdrop(model.interface)
        .environmentObject(model)
        .sheet(item: $pane.renamingSession) { RenameSessionSheet(model: model, session: $0) }
        .sheet(item: $pane.adoptingSession) { AdoptSessionSheet(model: model, session: $0) }
        .overlay(alignment: .top) {
            if model.paletteVisible {
                ZStack(alignment: .top) {
                    Color.black.opacity(0.18)
                        .ignoresSafeArea()
                        .onTapGesture { model.paletteVisible = false }
                    CommandPalette(model: model, pane: pane)
                        .padding(.top, 72)
                    // Escape closes the palette wherever focus is, not only from its field.
                    Button("Close palette") { model.paletteVisible = false }
                        .keyboardShortcut(.cancelAction)
                        .opacity(0)
                        .frame(width: 0, height: 0)
                        .accessibilityHidden(true)
                }
            }
        }
        .focusedSceneObject(pane)
        .background { if !snapshot { MainWindowReader(model: model) } }
        .onAppear { if !snapshot { model.windowAppeared() } }
        .onDisappear { if !snapshot { model.windowDisappeared() } }
    }

    @ViewBuilder private var splitView: some View {
        // The sidebar collapses to a column of no width rather than through
        // the split view's visibility: hiding and showing a column there
        // resizes the window to keep the conversation's width.
        let sidebar = pane.sidebarDocks ? DesktopColumns.sidebar : (min: 0, ideal: 0, max: 0)
        NavigationSplitView {
            Group {
                if pane.sidebarDocks {
                    SessionSidebar(model: model, pane: pane)
                        .belowWindowChrome()
                } else {
                    Color.clear
                }
            }
            .toolbar(removing: .sidebarToggle)
            .navigationSplitViewColumnWidth(min: sidebar.min, ideal: sidebar.ideal, max: sidebar.max)
        } detail: {
            ConversationPane(model: model, pane: pane)
                .opacity(model.isConnected || model.snapshot == nil ? 1 : 0.6)
                .hidesSplitDividers()
                .belowWindowChrome()
                .ignoresSafeArea(.container, edges: .top)
                .navigationSplitViewColumnWidth(min: DesktopColumns.detail.min, ideal: DesktopColumns.detail.ideal)
                .overlay(alignment: .trailing) {
                    if !pane.inspectorDocks && pane.inspectorOverlay && pane.displayedSession != nil {
                        SessionInspector(model: model, pane: pane)
                            .frame(width: DesktopColumns.inspector.ideal)
                            .belowWindowChrome()
                            .ignoresSafeArea(.container, edges: .top)
                            .shadow(color: .black.opacity(0.18), radius: 16, x: -4)
                            .transition(.move(edge: .trailing))
                    }
                }
                .inspector(isPresented: Binding(get: { pane.inspectorDocks && pane.inspectorVisible && pane.displayedSession != nil }, set: { if pane.inspectorDocks && pane.displayedSession != nil { pane.inspectorVisible = $0 } })) {
                    SessionInspector(model: model, pane: pane)
                        .belowWindowChrome()
                        .ignoresSafeArea(.container, edges: .top)
                        .inspectorColumnWidth(min: DesktopColumns.inspector.min, ideal: DesktopColumns.inspector.ideal, max: DesktopColumns.inspector.max)
                }
        }
        .overlay(alignment: .leading) {
            if !pane.sidebarDocks && pane.sidebarOverlay {
                ZStack(alignment: .leading) {
                    Color.black.opacity(0.001)
                        .onTapGesture { pane.closeSidebarOverlay() }
                    SessionSidebar(model: model, pane: pane)
                        .belowWindowChrome()
                        .frame(width: DesktopColumns.sidebar.ideal)
                        .shadow(color: .black.opacity(0.18), radius: 16, x: 4)
                        .transition(.move(edge: .leading))
                }
            }
        }
        .onChange(of: pane.selectedSessionID) { pane.closeSidebarOverlay() }
        .onChange(of: pane.newSession != nil) { pane.closeSidebarOverlay() }
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { pane.fitWindow(width: $0) }
    }
}

private struct WindowChromeLayout<Content: View>: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @ObservedObject var interface: InterfaceStore
    @ViewBuilder let content: Content

    var body: some View {
        let tabs = pane.tabsEnabled
        VStack(spacing: 0) {
            content
                .environment(\.windowChromeTop, tabs ? TitleBarStrip.height : 0)
                .overlay(alignment: .top) {
                    if tabs {
                        TitleBarStrip(model: model, pane: pane, interface: interface)
                            .ignoresSafeArea(.container, edges: .top)
                    }
                }
                .overlay(alignment: .topLeading) {
                    if !tabs && !pane.sidebarDocks {
                        SidebarToggle(pane: pane)
                            .frame(height: ConversationHeader.height)
                            .padding(.leading, TitleBarStrip.buttonsWidth)
                            .ignoresSafeArea(.container, edges: .top)
                    }
                }
                .overlay(alignment: .bottom) {
                    if let session = pane.archivedTab {
                        ArchivedTabNotice(pane: pane, session: session, label: interface.config.tabLabel)
                            .padding(.bottom, 132)
                            .transition(.opacity)
                    }
                }
            if !interface.config.statusItems.isEmpty { StatusStrip(model: model, pane: pane, interface: interface) }
        }
        .background { if tabs { WindowRectReader { _, window in TitleBarStrip.placeWindowButtons(in: window) } } }
    }
}

struct SessionSidebar: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @Environment(\.openWindow) private var openWindow
    @State private var expandedProjects: Set<String> = []
    @State private var expandedFamilies: Set<String> = []
    @ObservedObject private var shortcuts = ShortcutStore.shared

    var body: some View {
        let tree = model.sessionTree
        VStack(spacing: 0) {
            SessionSidebarHeader(
                model: model,
                counts: tree.counts,
                help: { shortcuts.help("\(SessionFacts.filterSummary($0, codexRecentHours: model.snapshot?.codexRecentHours ?? 5)).", $0.command) },
                newSessionHelp: shortcuts.help("New session", .newSession),
                findShortcut: shortcuts.keyboardShortcut(.findSession)
            )
            SessionRefinementBar(model: model)
                .scrimText("sidebar-refinement")

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    if !model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        searchResults
                    } else {
                    if !tree.needsYou.isEmpty {
                        VStack(alignment: .leading, spacing: 2) {
                            SidebarCaption("Needs you")
                            ForEach(tree.needsYou) { session in
                                sessionButton(session, title: SessionFamilies.title(session, in: model.knownSessions), attention: tree.attention[session.id], showsReason: true)
                            }
                        }
                    }
                    ForEach(tree.projects) { project in
                        let expanded = expandedProjects.contains(project.id)
                        let shown = expanded ? project : project.limited(to: SessionTree.collapsedSessionLimit, keeping: pane.selectedSessionID)
                        let single = project.checkouts.count == 1 ? project.checkouts.first : nil
                        VStack(alignment: .leading, spacing: 1) {
                            ProjectHeaderView(name: project.name, host: project.host, branch: single?.branchLabel, dirty: single?.dirty ?? false, archived: project.archived)
                                .contextMenu {
                                    if let identity = project.identity {
                                        Button(project.archived ? "Unarchive Project" : "Archive Project") { model.setProjectArchived(identity.id, !project.archived) }
                                    }
                                }
                            ForEach(shown.checkouts) { checkout in
                                if single == nil {
                                    CheckoutRowView(checkout: checkout)
                                }
                                ForEach(checkout.families) { family in
                                    familyRows(family, attention: tree.attention)
                                }
                            }
                            if project.sessionCount > shown.sessionCount || expanded && project.sessionCount > SessionTree.collapsedSessionLimit {
                                Button(expanded ? "Show less" : "Show \(project.sessionCount - shown.sessionCount) more") {
                                    if expanded { expandedProjects.remove(project.id) } else { expandedProjects.insert(project.id) }
                                }
                                .buttonStyle(.plain)
                                .font(.system(size: DesktopType.secondary))
                                .foregroundStyle(DesktopPalette.text2)
                                .padding(.leading, 35)
                                .padding(.vertical, DesktopSpace.xs)
                            }
                        }
                    }
                    if tree.projects.isEmpty {
                        Text(model.sessionRefinement.isActive ? "No sessions match these filters." : model.sessionFilter == .running ? "Nothing is running." : model.sessionFilter == .archived ? "Nothing is archived." : "No sessions yet.")
                            .font(.system(size: DesktopType.body))
                            .foregroundStyle(DesktopPalette.text2)
                            .padding(.horizontal, DesktopSpace.s)
                    }
                    }
                }
                .padding(.horizontal, DesktopSpace.s)
                .padding(.bottom, DesktopSpace.l)
                .scrimText("sidebar-rows")
            }

            ConnectionFooter(model: model)
                .scrimText("sidebar-footer")
        }
        .glassPane("sidebar", .sidebar, fallback: DesktopPalette.side)
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
        let attention = model.sessionAttention
        VStack(alignment: .leading, spacing: 2) {
            SidebarCaption("Sessions")
            ForEach(local) { session in
                sessionButton(session, title: SessionFamilies.title(session, in: model.knownSessions), attention: attention[session.id])
            }
            if local.isEmpty {
                Text("No matching sessions.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.horizontal, 14)
                    .padding(.vertical, DesktopSpace.xs)
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
                                    .font(.system(size: DesktopType.meta))
                                    .lineLimit(2)
                                    .foregroundStyle(DesktopPalette.text2)
                            }
                        }
                        .font(.system(size: DesktopType.body))
                        .padding(.vertical, 6)
                        .padding(.horizontal, 14)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(pane.selectedSessionID == result.session.id ? DesktopPalette.selection : Color.clear, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
                if let error = model.searchError {
                    Text(error)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, 14)
                } else if !model.searching && remote.isEmpty {
                    Text("No older matches.")
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(.horizontal, 14)
                        .padding(.vertical, DesktopSpace.xs)
                }
            }
        }
    }

    private var searchMatches: (local: [SessionState], remote: [SessionSearchItem]) {
        let query = model.searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
        let local = SessionTree.newestFirst(model.knownSessions.filter { session in
            [session.title, session.name, session.project?.displayName, session.checkout?.branch]
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
    private func familyRows(_ family: SessionFamily, attention: [String: SessionAttention]) -> some View {
        let expanded = familyExpanded(family)
        HStack(spacing: 0) {
            sessionButton(family.root, title: family.root.title, family: family, attention: attention[family.root.id])
            if !family.members.isEmpty {
                Button {
                    if expandedFamilies.contains(family.id) { expandedFamilies.remove(family.id) } else { expandedFamilies.insert(family.id) }
                } label: {
                    Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        .font(.system(size: DesktopType.caption, weight: .semibold))
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
                sessionButton(member.session, title: SessionFamilies.label(member.session), attention: attention[member.id])
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

    private func sessionButton(_ session: SessionState, title: String, family: SessionFamily? = nil, attention: SessionAttention?, showsReason: Bool = false) -> some View {
        let selected = pane.selectedSessionID == session.id
        let needsYou = attention != nil
        return Button {
            pane.select(session.id, keep: NSEvent.modifierFlags.contains(.command))
        } label: {
            SessionRowView(session: session, title: title, family: family, needsYou: needsYou, reason: showsReason ? attention?.reason : nil, finishedUnseen: model.finishedUnseen.contains(session.id))
                .padding(.vertical, 6)
                .padding(.horizontal, DesktopSpace.s)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(selected ? DesktopPalette.selection : (needsYou ? DesktopPalette.attentionBackground : Color.clear), in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .simultaneousGesture(TapGesture(count: 2).onEnded { pane.keepTab(session.id) })
        .accessibilityAddTraits(selected ? .isSelected : [])
        .contextMenu {
            Button("Open in New Window") { openWindow(id: "session", value: session.id) }
            Button("Rename…") { pane.renamingSession = session }
            if session.canAdopt {
                Button("Open in Agenthail") { pane.adoptingSession = session }
            }
            Divider()
            Button(session.archived == true ? "Unarchive" : "Archive") { model.setArchived(sessionID: session.id, session.archived != true) }
            if session.project == nil, session.remoteHostName == nil, let cwd = session.cwd, !cwd.isEmpty {
                Button("Add Folder as Project") { model.addProject(path: cwd) }
            }
        }
    }
}

struct SidebarCaption: View {
    let text: String
    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text)
            .font(.system(size: DesktopType.caption, weight: .semibold))
            .foregroundStyle(DesktopPalette.text2)
            .padding(.horizontal, DesktopSpace.s)
            .padding(.vertical, DesktopSpace.xs)
    }
}

struct ProjectHeaderView: View {
    let name: String
    var host: String? = nil
    var branch: String? = nil
    var dirty = false
    var archived = false

    var body: some View {
        HStack(spacing: 7) {
            Text(String(name.prefix(1)).uppercased())
                .font(.system(size: DesktopType.caption, weight: .semibold))
                .foregroundStyle(DesktopPalette.text2)
                .frame(width: 18, height: 18)
                .background(DesktopPalette.fill, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                .accessibilityHidden(true)
            Text(name)
                .font(.system(size: DesktopType.body, weight: .semibold))
                .foregroundStyle(DesktopPalette.text)
                .lineLimit(1)
            if let host {
                HostBadge(name: host)
            }
            if let branch {
                Text(branch)
                    .font(.system(size: DesktopType.caption, design: .monospaced))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            if dirty {
                Circle().fill(DesktopPalette.muted).frame(width: 5, height: 5)
                    .accessibilityLabel("Uncommitted changes")
            }
        }
        .padding(.horizontal, DesktopSpace.s)
        .padding(.vertical, 5)
    }
}

/// A one-line host label for remote sessions and projects. It truncates
/// rather than wrapping so it never squeezes the row's status.
struct HostBadge: View {
    let name: String
    var unavailable = false

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: unavailable ? "wifi.slash" : "server.rack")
            Text(name)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .font(.system(size: DesktopType.caption))
        .foregroundStyle(DesktopPalette.text2)
        .frame(maxWidth: 96, alignment: .leading)
        .fixedSize(horizontal: false, vertical: true)
    }
}

struct CheckoutRowView: View {
    let checkout: SessionTree.Checkout

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "arrow.triangle.branch")
                .font(.system(size: DesktopType.caption))
            Text(checkout.label)
                .font(.system(size: DesktopType.caption, design: .monospaced))
                .lineLimit(1)
                .truncationMode(.middle)
            if checkout.dirty {
                Circle().fill(DesktopPalette.muted).frame(width: 5, height: 5)
                    .accessibilityLabel("Uncommitted changes")
            }
        }
        .foregroundStyle(DesktopPalette.text2)
        .padding(.leading, 33)
        .padding(.trailing, DesktopSpace.s)
        .padding(.top, DesktopSpace.xs)
        .padding(.bottom, 2)
    }
}

struct SessionRowView: View {
    let session: SessionState
    var title: String? = nil
    var family: SessionFamily? = nil
    let needsYou: Bool
    /// Why the session needs the person, shown under its title.
    var reason: String? = nil
    var finishedUnseen = false

    var body: some View {
        HStack(alignment: reason == nil ? .center : .firstTextBaseline, spacing: 9) {
            StatusIndicator(session: session, needsYou: needsYou, finishedUnseen: finishedUnseen)
            VStack(alignment: .leading, spacing: 2) {
                Text(title ?? session.title)
                    .lineLimit(1)
                    .foregroundStyle(!session.isUnavailable && (session.open || session.isWorking) ? DesktopPalette.text : DesktopPalette.text2)
                if let reason {
                    Text(reason)
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                        .lineLimit(1)
                        .truncationMode(.tail)
                } else if session.isWorking, !session.isWaitingOnYou, let activity = session.activity {
                    TimelineView(.everyMinute) { context in
                        if let note = activity.quietNote(now: context.date) {
                            Text(note)
                                .font(.system(size: DesktopType.meta))
                                .foregroundStyle(DesktopPalette.text2)
                                .lineLimit(1)
                                .truncationMode(.tail)
                        }
                    }
                }
            }
            if let host = session.shortRemoteHostName {
                HostBadge(name: host, unavailable: session.isUnavailable)
                    .help(session.unavailableReason ?? "On \(session.remoteHostName ?? host)")
            }
            Spacer(minLength: 4)
            if let family, family.subagentCount > 0 {
                HStack(spacing: 3) {
                    Image(systemName: "person.2")
                    Text(family.workingSubagents > 0 ? "\(family.workingSubagents)/\(family.subagentCount)" : "\(family.subagentCount)")
                }
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(family.workingSubagents > 0 ? DesktopPalette.working : DesktopPalette.text2)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(SessionFamilies.subagentSummary(family))
            }
            if session.queueCount > 0 {
                Text(SessionFacts.queuedLabel(session.queueCount))
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
            }
            if !session.sharedHolders.isEmpty {
                HStack(spacing: 3) {
                    Image(systemName: "terminal")
                    Text("\(session.sharedHolders.count)")
                }
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.text2)
                .help(SessionFacts.holdersHelp(session.sharedHolders))
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(SessionFacts.holdersLabel(session.sharedHolders.count))
            }
            Text(relativeAge(session.lastActive))
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.text2)
        }
        .font(.system(size: DesktopType.body))
        .padding(.leading, 6)
        .accessibilityElement(children: .combine)
        .accessibilityValue(needsYou ? "Needs you\(reason.map { ": \($0)" } ?? "") · \(SessionFacts.rowDetail(session))" : SessionFacts.rowDetail(session))
        .help(SessionFacts.rowDetail(session))
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
                    .tint(DesktopPalette.working)
                    .scaleEffect(0.8)
            } else if session.isWaitingOnBackground && !needsYou {
                Circle()
                    .strokeBorder(DesktopPalette.working, lineWidth: 1.5)
                    .frame(width: 7, height: 7)
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
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @EnvironmentObject private var voice: VoiceController
    @Environment(\.openWindow) private var openWindow

    private var offlineLabel: String {
        if model.daemonSlow { return "Agenthail is slow to respond" }
        if model.loading && model.connectionError == nil { return "Loading sessions…" }
        guard model.snapshot != nil, let loadedAt = model.snapshotLoadedAt else { return "Agenthail isn't running" }
        return "Offline · as of \(relativeAge(loadedAt.formatted(.iso8601)))"
    }

    private var syncedLabel: String {
        let age = model.snapshotLoadedAt.map { relativeAge($0.formatted(.iso8601)) } ?? ""
        let synced = age.isEmpty ? "Not synced yet" : age == "now" ? "Synced just now" : "Synced \(age) ago"
        return "\(synced). \(shortcuts.help("Refresh", .refresh))"
    }

    var body: some View {
        HStack(spacing: DesktopSpace.s) {
            Circle()
                .fill(model.isConnected ? DesktopPalette.done : DesktopPalette.failed)
                .frame(width: 6, height: 6)
                .accessibilityHidden(true)
            Text(model.isConnected ? "Connected · this Mac" : model.reconnecting ? "Reconnecting…" : offlineLabel)
            Spacer()
            if model.isConnected, let notice = model.daemonBuildNotice {
                Button("Restart for this version") { model.restartDaemon() }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.failed)
                    .help(notice)
            }
            if !model.isConnected && !model.reconnecting && !model.daemonSlow {
                Button("Start") { model.restartDaemon() }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accentText)
                    .help("Start the Agenthail daemon")
            }
            if model.isConnected {
                Button {
                    voice.route(to: "")
                    openWindow(id: "voice")
                } label: {
                    Image(systemName: "waveform")
                }
                .buttonStyle(.plain)
                .help("Talk to orchestrator")
                .accessibilityLabel("Talk to orchestrator")
                Button { model.refreshNow() } label: {
                    Image(systemName: "arrow.triangle.2.circlepath")
                }
                .buttonStyle(.plain)
                .help(syncedLabel)
                .accessibilityLabel("Refresh")
                .accessibilityValue(syncedLabel)
            }
            SettingsLink {
                Image(systemName: "slider.horizontal.3")
            }
            .buttonStyle(.plain)
            .help("Settings ⌘,")
            .accessibilityLabel("Settings")
        }
        .font(.system(size: DesktopType.meta))
        .foregroundStyle(DesktopPalette.text2)
        .padding(.horizontal, DesktopSpace.l)
        .padding(.vertical, 10)
        .overlay(alignment: .top) { Rectangle().fill(DesktopPalette.line).frame(height: 1) }
    }
}

struct ConversationPane: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    var headerInset: CGFloat = 22
    @EnvironmentObject private var voice: VoiceController
    @Environment(\.windowChromeTop) private var chromeTop
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        if let session = pane.displayedSession {
            TranscriptView(model: model, pane: pane, session: session)
                .task(id: SessionPane.requestSyncKey(session)) { await pane.syncRequests(for: session) }
                .safeAreaInset(edge: .top, spacing: 0) {
                    ConversationHeader(session: session, title: SessionFamilies.title(session, in: model.knownSessions), model: pane.detail?.modelLabel, context: pane.detail?.context, inspectorVisible: Binding(get: { pane.inspectorShown }, set: { _ in pane.toggleInspector() }), leadingInset: pane.sidebarDocks || chromeTop > 0 ? headerInset : TitleBarStrip.buttonsWidth + 32, onFocusTerminal: { model.focusInTerminal(session) }, onCall: pane.removedSession?.id == session.id || !session.canCallByVoice ? nil : {
                        voice.route(to: session.id)
                        NSApplication.shared.activate()
                        openWindow(id: "voice")
                    })
                    .scrimText("conversation-header")
                    .backdropSurface(DesktopPalette.window, paneID: "content")
                }
                .safeAreaInset(edge: .bottom, spacing: 0) {
                    if pane.removedSession?.id == session.id {
                        RemovedSessionBar(onClose: pane.closeRemovedSession)
                    } else {
                        VStack(spacing: 0) {
                            if let waiting = session.waitingOn, ElsewhereWaitCard.shows(waiting, readOnly: session.isReadOnly, respond: session.capabilities.respond) {
                                ElsewhereWaitCard(waiting: waiting, place: session.waitingPlace ?? .elsewhere, surface: session.surface, hostName: session.runtime?.hostName, accent: DesktopPalette.accentText,
                                                  open: session.runtime?.focusable == true ? { model.focusInTerminal(session) } : nil,
                                                  restart: { model.stopAndResume(session.id) })
                                    .id(waiting.id)
                                    .scrimText("dock-wait")
                                    .frame(maxWidth: 720)
                                    .padding(.horizontal, DesktopSpace.l)
                                    .padding(.top, DesktopSpace.s)
                            } else if let request = pane.requests?.requests.first, !session.isReadOnly {
                                RequestCard(request: request, answering: pane.answeringRequest != nil, accent: DesktopPalette.accentText) { decision in
                                    Task { await pane.respond(to: request, decision: decision) }
                                }
                                .id(request.id)
                                .scrimText("dock-request")
                                .frame(maxWidth: 720)
                                .padding(.horizontal, DesktopSpace.l)
                                .padding(.top, DesktopSpace.s)
                            }
                            if let error = pane.requestError {
                                Text(error).font(.footnote).foregroundStyle(.secondary).padding(.horizontal, DesktopSpace.l).padding(.top, DesktopSpace.xs)
                                    .frame(maxWidth: .infinity)
                                    .backdropSurface(DesktopPalette.window, paneID: "content")
                            }
                            ComposerView(model: model, pane: pane, session: session)
                        }
                    }
                }
            .glassPane("content", .content, fallback: DesktopPalette.window)
            .environmentObject(pane)
        } else if let draft = pane.newSession {
            NewSessionView(model: model, draft: draft)
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
            .fixedSize()
            .scrimText("empty")
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .backdropSurface(DesktopPalette.window)
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
                    .font(.system(size: DesktopType.body, weight: .medium))
                    .foregroundStyle(DesktopPalette.text)
                Text("Agenthail stopped finding it. You're reading its last loaded content.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Spacer()
            Button("Close", action: onClose)
        }
        .padding(.horizontal, DesktopSpace.l)
        .padding(.vertical, DesktopSpace.m)
        .background(DesktopPalette.dock, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).stroke(DesktopPalette.line))
        .frame(maxWidth: 760)
        .padding(.horizontal, DesktopSpace.xxl)
        .padding(.bottom, DesktopSpace.l)
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
    var onCall: (() -> Void)?

    static let height: CGFloat = 52

    var body: some View {
        HStack(spacing: DesktopSpace.m) {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Text(session.subagent != nil ? title : session.alias.map { _ in session.name.isEmpty ? session.title : session.name } ?? title)
                    .font(.system(size: DesktopType.body, weight: .semibold))
                if let alias = session.alias, !alias.isEmpty {
                    Text("@\(alias)")
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            .lineLimit(1)
            Spacer()
            HStack(spacing: 6) {
                StatusIndicator(session: session, needsYou: false)
                TimelineView(.everyMinute) { context in
                    Text(session.waitingPlace?.label ?? (session.isWorking ? session.workingLabel(now: context.date) : session.limitLabel ?? (session.isWaitingOnBackground ? "Waiting on background work" : session.open ? "Idle" : "Closed")))
                }
            }
            .font(.system(size: DesktopType.secondary))
            .foregroundStyle(DesktopPalette.text2)
            let meta = ([session.surface.capitalized, model] + SessionFacts.headerFacts(session, age: relativeAge(session.lastActive)) + [context?.headerLabel]).compactMap { $0 }.joined(separator: " · ")
            Text(meta)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
                .lineLimit(1)
                .truncationMode(.middle)
                .help(meta)
            if session.runtime?.focusable == true, let host = session.runtime?.hostName {
                Button("Open in \(host)") { onFocusTerminal() }
                    .buttonStyle(.plain)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.accentText)
                    .help("Bring this session's terminal to the front")
            }
            if let onCall {
                Button(action: onCall) {
                    Image(systemName: "waveform")
                        .frame(width: 30, height: 30)
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
                    .frame(width: 30, height: 30)
                    .background(inspectorVisible ? DesktopPalette.selection : Color.clear, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .help(shortcuts.help("Inspector", .toggleInspector))
            .accessibilityLabel("Toggle inspector")
            .accessibilityValue(inspectorVisible ? "Shown" : "Hidden")
        }
        .padding(.leading, leadingInset)
        .padding(.trailing, 14)
        .frame(height: Self.height)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
    }
}

struct TranscriptView: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var following = true
    @State private var underfilled = false
    @State private var textHeight: CGFloat = 0
    private static let bottomPadding: CGFloat = 12
    @ObservedObject private var shortcuts = ShortcutStore.shared

    var body: some View {
        let blocks = TranscriptBlock.build(pane.timelineItems)
        let sends = model.localSends[session.id] ?? []
        let canResend = pane.removedSession?.id != session.id && session.capabilities.send && !session.isReadOnly
        ScrollView {
            LazyVStack(alignment: .leading, spacing: DesktopSpace.xl) {
                if (pane.olderCursor ?? 0) > 0 || pane.olderError != nil {
                    olderControl
                }
                if let error = pane.detail?.readError {
                    Text(error)
                        .font(.system(size: DesktopType.body))
                        .foregroundStyle(DesktopPalette.text2)
                }
                if blocks.isEmpty && sends.isEmpty && !session.isWorking && pane.detail != nil && (pane.olderCursor ?? 0) == 0 {
                    VStack(spacing: 6) {
                        Text("No saved exchanges yet")
                            .font(.system(size: DesktopType.title, weight: .semibold))
                        Text("Send a message to start this conversation from Agenthail.")
                            .font(.system(size: DesktopType.body))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                    .frame(maxWidth: .infinity)
                    .padding(.top, 60)
                }
                ForEach(blocks) { block in
                    TranscriptBlockView(block: block, onEdit: canResend ? { resend($0) } : nil)
                }
                ForEach(sends) { send in
                    UserBubble(text: send.text, receipt: send.status, files: send.files, failure: send.failure, onEdit: canResend ? { text in
                        if send.failure != nil { model.discardSend(send, in: session.id) }
                        resend(text, attachments: send.failure != nil ? send.attachments : [])
                    } : nil, onRetry: send.failure == nil || !canResend ? nil : { model.retrySend(send, in: session.id) })
                }
                if session.isWorking {
                    HStack(spacing: DesktopSpace.s) {
                        StatusIndicator(session: session, needsYou: false)
                        TimelineView(.everyMinute) { context in
                            Text(session.waitingPlace?.label ?? session.workingLabel(now: context.date))
                        }
                    }
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
                } else if session.isWaitingOnBackground, let background = session.backgroundTasks {
                    HStack(spacing: DesktopSpace.s) {
                        StatusIndicator(session: session, needsYou: false)
                        Text("Waiting on background work · \(background.summary)")
                    }
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
                }
            }
            .frame(maxWidth: 700)
            .padding(.horizontal, DesktopSpace.xxl)
            .padding(.top, 28)
            .padding(.bottom, Self.bottomPadding)
            .frame(maxWidth: .infinity)
            .environment(\.transcriptRows, TranscriptRows(memory: pane.rowMemory, session: session.id))
        }
        .background {
            Color.clear
                .frame(maxWidth: 700)
                .frame(height: textHeight)
                .scrimText("transcript-column")
                .frame(maxHeight: .infinity, alignment: .top)
        }
        .onScrollGeometryChange(for: CGFloat.self) { geometry in
            min(geometry.contentSize.height - Self.bottomPadding - max(geometry.contentOffset.y, 0), geometry.containerSize.height).rounded()
        } action: { _, height in
            textHeight = max(height, 0)
        }
        .transcriptFollow(session.id, leadingRow: pane.timelineItems.first?.id, hasRow: { id in pane.timelineItems.contains { $0.id == id } }, position: $position, following: $following)
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentSize.height < geometry.containerSize.height * 1.5
        } action: { _, value in
            underfilled = value
            if value { fillViewport() }
        }
        .onChange(of: pane.olderItems.count) { if underfilled { fillViewport() } }
        .onChange(of: pane.detail?.session.id) { if underfilled { fillViewport() } }
        .overlay(alignment: .bottomTrailing) {
            if !following {
                Button {
                    jumpToLatest()
                } label: {
                    Label("Jump to latest", systemImage: "arrow.down")
                        .font(.system(size: DesktopType.secondary))
                        .padding(.horizontal, 10)
                        .padding(.vertical, 6)
                        .background(DesktopPalette.raised, in: Capsule())
                        .overlay(Capsule().strokeBorder(DesktopPalette.line))
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .padding(DesktopSpace.l)
            }
        }
        .background {
            Button("", action: jumpToLatest)
                .keyboardShortcut(shortcuts.keyboardShortcut(.jumpToLatest))
                .hidden()
        }
        .overlay {
            if pane.detail == nil, pane.selectedSessionID == session.id {
                VStack(spacing: DesktopSpace.s) {
                    ProgressView().controlSize(.small)
                    if let error = pane.detailLoadError {
                        Text("Couldn't load this session. Retrying… \(error)")
                            .font(.system(size: DesktopType.secondary))
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
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.text2)
                .padding(.horizontal, 10)
                .padding(.vertical, DesktopSpace.xs)
                .background(DesktopPalette.raised, in: Capsule())
                .overlay(Capsule().stroke(DesktopPalette.line))
                .padding(.top, DesktopSpace.s)
                .transition(.opacity)
            }
        }
    }

    private var olderControl: some View {
        HStack(spacing: DesktopSpace.s) {
            if pane.loadingOlder {
                ProgressView().controlSize(.mini)
            } else if (pane.olderCursor ?? 0) > 0 {
                Button("Load earlier") {
                    Task { await pane.loadOlder() }
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.accentText)
            }
            if let error = pane.olderError {
                Text(error).foregroundStyle(DesktopPalette.text2)
            }
        }
        .font(.system(size: DesktopType.secondary))
        .frame(maxWidth: .infinity)
    }

    private func fillViewport() {
        guard pane.detail != nil, let cursor = pane.olderCursor, cursor > 0, !pane.loadingOlder, pane.olderError == nil else { return }
        Task { await pane.loadOlder() }
    }

    private func resend(_ text: String, attachments: [ComposerAttachment] = []) {
        pane.resend(text, attachments: attachments)
        jumpToLatest()
    }

    private func jumpToLatest() {
        scrollToLatest($position, following: $following)
    }
}

struct TranscriptBlockView: View {
    let block: TranscriptBlock
    var onEdit: ((String) -> Void)? = nil
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace

    private var note: String? { block.truncated ? TimelineMetadata.shortened : nil }
    private var expanded: Bool { rows.current(opened) ?? rows.isOpen(block.id) ?? false }

    var body: some View {
        switch block.kind {
        case .user(let text, let files):
            let envelope = PeerEnvelope(text)
            if let sender = envelope.sender {
                PeerMessage(sender: sender, text: envelope.body, time: TurnClock.time(block.timestamp), note: note)
            } else {
                UserBubble(text: text, receipt: note, time: TurnClock.time(block.timestamp), files: files, onEdit: onEdit, row: block.id)
            }
        case .assistant(let text):
            AssistantReply(text: text, note: note, footer: block.finished.map { TurnClock.footer(duration: $0, at: block.timestamp) })
        case .peer(let sender, let text):
            PeerMessage(sender: sender, text: text, time: TurnClock.time(block.timestamp), note: note)
        case .tools(let items):
            ToolRunView(id: block.id, items: items)
        default:
            VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                record
                if let note {
                    Text(note)
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.muted)
                }
            }
        }
    }

    private func toggle() {
        holdPlace()
        let next = !expanded
        opened = rows.choice(next)
        rows.set(block.id, open: next)
    }

    @ViewBuilder private var record: some View {
        switch block.kind {
        case .user, .assistant, .peer, .tools:
            EmptyView()
        case .annotation(let text):
            Text(text)
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.muted)
        case .notice(let text):
            Button(action: toggle) {
                HStack(spacing: DesktopSpace.s) {
                    Rectangle().fill(DesktopPalette.line).frame(width: 18, height: 1)
                    Text(text)
                        .lineLimit(expanded ? nil : 1)
                        .multilineTextAlignment(.leading)
                        .layoutPriority(1)
                    Rectangle().fill(DesktopPalette.line).frame(minWidth: 18, maxWidth: .infinity, maxHeight: 1)
                }
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.muted)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(expanded ? "" : text)
            .accessibilityValue(expanded ? "Expanded" : "Collapsed")
        case .image(let attachment):
            AttachmentImageView(attachment: attachment)
        case .instructions(let text):
            VStack(alignment: .leading, spacing: 6) {
                Button(action: toggle) {
                    HStack(spacing: 6) {
                        Image(systemName: expanded ? "chevron.down" : "chevron.right")
                        Text("Instructions")
                    }
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.muted)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityHint("Shows what the agent was told")
                if expanded {
                    Text(text)
                        .font(.system(size: DesktopType.meta, design: .monospaced))
                        .foregroundStyle(DesktopPalette.muted)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
        }
    }
}

struct AttachmentImageView: View {
    @EnvironmentObject private var model: AgenthailModel
    @EnvironmentObject private var pane: SessionPane
    let attachment: SessionAttachment
    @State private var image: NSImage?
    @State private var failed = false
    @State private var retryCount = 0

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .interpolation(.high)
                    .aspectRatio(contentMode: .fit)
                    .clipShape(RoundedRectangle(cornerRadius: DesktopRadius.card))
                    .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(DesktopPalette.line))
                    .frame(maxWidth: 520, maxHeight: 360, alignment: .leading)
                    .onTapGesture(count: 2) { open(image) }
                    .contextMenu {
                        Button("Open in Preview") { open(image) }
                        Button("Copy image") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.writeObjects([image])
                        }
                    }
            } else if failed {
                HStack(spacing: DesktopSpace.s) {
                    Text("Image couldn't be loaded")
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.muted)
                    Button("Try again") {
                        failed = false
                        retryCount += 1
                    }
                    .buttonStyle(.link)
                    .font(.system(size: DesktopType.meta))
                }
            } else {
                RoundedRectangle(cornerRadius: DesktopRadius.card)
                    .fill(DesktopPalette.bubble)
                    .frame(width: 220, height: 140)
                    .overlay { ProgressView().controlSize(.small) }
            }
        }
        .task(id: "\(attachment.id)-\(retryCount)") { await load() }
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
    var time: String? = nil
    var files: [AttachedFile] = []
    var failure: String? = nil
    var onEdit: ((String) -> Void)? = nil
    var onRetry: (() -> Void)? = nil
    /// The transcript row this message is, so "Show more" is remembered with the session's other rows.
    var row: String? = nil
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
    @Environment(\.holdTranscriptPlace) private var holdPlace
    @State private var editing = false
    @State private var hovering = false

    private var expanded: Bool { rows.current(opened) ?? row.flatMap { rows.isOpen("more-" + $0) } ?? false }

    var body: some View {
        VStack(alignment: .trailing, spacing: 5) {
            if editing, let onEdit {
                MessageEditor(text: text, onCancel: { editing = false }, onSend: { edited in
                    editing = false
                    onEdit(edited)
                })
            } else {
                bubble
            }
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .padding(.leading, 120)
        .onHover { hovering = $0 }
    }

    @ViewBuilder private var bubble: some View {
        if !files.isEmpty {
            HStack(spacing: DesktopSpace.s) {
                ForEach(Array(files.enumerated()), id: \.offset) { _, file in AttachedFileChip(file: file) }
            }
        }
        if !text.isEmpty {
            Text(text)
                .font(.system(size: DesktopType.prose))
                .lineSpacing(DesktopType.proseLineSpacing)
                .lineLimit(expanded ? nil : 8)
                .textSelection(.enabled)
                .padding(.horizontal, 13)
                .padding(.vertical, 9)
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.sheet))
        }
        if text.split(separator: "\n", omittingEmptySubsequences: false).count > 8 || text.count > 700 {
            Button(expanded ? "Show less" : "Show more") {
                holdPlace()
                opened = rows.choice(!expanded)
                if let row { rows.set("more-" + row, open: !expanded) }
            }
                .buttonStyle(.plain)
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.text2)
        }
        HStack(spacing: DesktopSpace.s) {
            MessageActions(text: text, revealed: hovering, onEdit: onEdit == nil ? nil : { editing = true })
            if let failure {
                Image(systemName: "exclamationmark.circle")
                    .foregroundStyle(DesktopPalette.failed)
                Text("Not sent · \(failure)")
                    .foregroundStyle(DesktopPalette.failed)
                    .lineLimit(2)
                if let onRetry {
                    Button("Retry", action: onRetry)
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.accentText)
                }
            } else {
                let status = [receipt, time].compactMap { $0 }.joined(separator: " · ")
                if !status.isEmpty { Text(status) }
            }
        }
        .font(.system(size: DesktopType.caption))
        .foregroundStyle(DesktopPalette.text2)
    }
}

/// A message another agent sent this session: on the reader's side, marked by a rule and its sender.
struct PeerMessage: View {
    let sender: String
    let text: String
    var time: String? = nil
    var note: String? = nil
    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(spacing: DesktopSpace.s) {
                Text(sender)
                    .font(.system(size: DesktopType.meta, weight: .semibold))
                    .foregroundStyle(DesktopPalette.working)
                let status = [time, note].compactMap { $0 }.joined(separator: " · ")
                if !status.isEmpty {
                    Text(status)
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.muted)
                }
                MessageActions(text: text, revealed: hovering)
            }
            .accessibilityElement(children: .contain)
            .accessibilityLabel("From \(sender)")
            Text(text)
                .font(.system(size: DesktopType.prose))
                .lineSpacing(DesktopType.proseLineSpacing)
                .foregroundStyle(DesktopPalette.text)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.leading, DesktopSpace.m)
        .overlay(alignment: .leading) { Rectangle().fill(DesktopPalette.working).frame(width: 2) }
        .onHover { hovering = $0 }
    }
}

struct AttachedFileChip: View {
    let file: AttachedFile
    @State private var copied = false

    var body: some View {
        let local = !file.path.isEmpty && FileManager.default.fileExists(atPath: file.path)
        Button {
            if local {
                NSWorkspace.shared.open(URL(fileURLWithPath: file.path))
            } else {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(file.path, forType: .string)
                copied = true
            }
        } label: {
            HStack(spacing: 7) {
                Image(systemName: "doc")
                    .foregroundStyle(DesktopPalette.text2)
                Text(file.name)
                    .foregroundStyle(DesktopPalette.text)
                if let size = file.sizeLabel {
                    Text(size)
                        .foregroundStyle(DesktopPalette.text2)
                }
                if copied {
                    Text("Path copied")
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            .font(.system(size: DesktopType.body))
            .lineLimit(1)
            .padding(.horizontal, 11)
            .padding(.vertical, 7)
            .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        }
        .buttonStyle(.plain)
        .disabled(file.path.isEmpty)
        .help(file.path.isEmpty ? file.name : (local ? "Open \(file.path)" : "Copy \(file.path)"))
        .accessibilityLabel(local ? "Open \(file.name)" : "Copy the path of \(file.name)")
        .task(id: copied) {
            guard copied else { return }
            try? await Task.sleep(for: .seconds(2))
            copied = false
        }
    }
}

struct AssistantReply: View {
    let text: String
    var note: String? = nil
    /// "Finished 3:13 · 6:10 PM" when this reply ended its turn; Copy sits on that line.
    var footer: String? = nil
    @State private var hovering = false

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            HStack(spacing: DesktopSpace.s) {
                Image(systemName: "sparkle")
                    .font(.system(size: 10))
                    .foregroundStyle(DesktopPalette.muted)
                    .accessibilityHidden(true)
                if footer == nil {
                    MessageActions(text: text, revealed: hovering)
                }
            }
            .frame(height: MessageActions.height)
            StructuredText(markdown: text)
                .font(.system(size: DesktopType.prose))
                .lineSpacing(DesktopType.proseLineSpacing)
                .foregroundStyle(DesktopPalette.text)
                .textual.textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            if note != nil || footer != nil {
                HStack(spacing: DesktopSpace.s) {
                    let status = [note, footer].compactMap { $0 }.joined(separator: " · ")
                    if !status.isEmpty {
                        Text(status)
                            .font(.system(size: DesktopType.caption))
                            .foregroundStyle(DesktopPalette.muted)
                    }
                    if footer != nil {
                        MessageActions(text: text, revealed: hovering)
                    }
                }
            }
        }
        .onHover { hovering = $0 }
    }
}

struct ComposerView: View {
    @ObservedObject var model: AgenthailModel
    let pane: SessionPane
    let session: SessionState
    @State private var focused = false
    @State private var dropTargeted = false
    @State private var dismissedSlash: String?
    @State private var modeChanged = false
    @State private var showingSent = false
    @State private var searchingSent = false
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
    private var canSteer: Bool { session.capabilities.steer && !session.isWaitingOnYou }
    private var pasteAttachments: ((PastedAttachments) -> Void)? {
        guard session.capabilities.attach else { return nil }
        return { add($0) }
    }

    var body: some View {
        VStack(spacing: 0) {
            let attention = QueueFacts.attention(model.snapshot?.attention ?? [], for: session.id)
            if !attention.isEmpty {
                BoundedDock {
                    VStack(spacing: 0) {
                        ForEach(attention) { AttentionBanner(model: model, attention: $0).padding(.bottom, DesktopSpace.s) }
                    }
                }
                .scrimText("dock-attention")
            }
            if session.isReadOnly || !session.capabilities.send {
                Text(session.readOnlyReason ?? "This session is closed. You can read it, but not message it.")
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 15)
                    .padding(.vertical, 13)
                    .background(DesktopPalette.window, in: RoundedRectangle(cornerRadius: DesktopRadius.sheet))
                    .overlay(RoundedRectangle(cornerRadius: DesktopRadius.sheet).strokeBorder(DesktopPalette.line, style: StrokeStyle(lineWidth: 1, dash: [4])))
            } else {
                let queued = model.queuedItems(for: session.id)
                BoundedDock {
                    VStack(spacing: 0) {
                        ForEach(model.deliveryProblems.filter { $0.sessionId == session.id }) { problem in
                            DeliveryProblemBanner(problem: problem, model: model)
                                .padding(.bottom, DesktopSpace.s)
                        }
                        if !session.composerTurnNotice.isEmpty {
                            Text(session.composerTurnNotice)
                                .font(.system(size: DesktopType.caption))
                                .foregroundStyle(DesktopPalette.text2)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal, 15)
                                .padding(.bottom, DesktopSpace.s)
                        }
                        if !queued.isEmpty {
                            QueueDock(model: model, items: queued, target: session, canSteer: canSteer && session.isWorking)
                                .padding(.horizontal, 10)
                        }
                        if !draft.attachments.isEmpty {
                            AttachmentDock(attachments: $draft.attachments, roundedTop: queued.isEmpty)
                                .padding(.horizontal, 10)
                        }
                    }
                }
                .scrimText("dock-notices")
                if !slashSuggestions.isEmpty {
                    SlashCommandMenu(suggestions: slashSuggestions, roundedTop: queued.isEmpty) { choice in
                        draft.text = choice
                        focused = true
                    }
                    .padding(.horizontal, 10)
                }
                VStack(spacing: 0) {
                    ComposerTextView(text: $draft.text, focused: $focused, placeholder: "Message \(session.title)", onPaste: pasteAttachments, onCommand: perform, onUpFromFirstLine: { takeBackQueued() || olderPrompt() }, onDownFromLastLine: newerPrompt) { submit(alternate: false) }
                        .id(session.id)
                        .padding(.horizontal, 15)
                        .padding(.top, 13)
                        .padding(.bottom, DesktopSpace.xs)
                    HStack(spacing: DesktopSpace.xs) {
                        if session.capabilities.attach {
                            Button(action: chooseAttachments) {
                                Image(systemName: "paperclip")
                                    .font(.system(size: DesktopType.body))
                                    .frame(width: 28, height: 28)
                            }
                            .buttonStyle(.plain)
                            .foregroundStyle(DesktopPalette.text2)
                            .help("Attach files or images")
                            .accessibilityLabel("Attach files or images")
                            .padding(.leading, 5)
                        }
                        if session.surface == "codex" {
                            TurnSettingsMenu(model: model, sessionID: session.id, detail: pane.detail)
                        }
                        if let mode = permissionMode {
                            Button(action: cycleMode) {
                                Text(SessionRequests.permissionModeLabel(mode))
                                    .font(.system(size: DesktopType.meta, weight: .medium))
                                    .foregroundStyle(mode == "" || mode == "manual" ? DesktopPalette.text2 : DesktopPalette.accentText)
                                    .padding(.horizontal, 8)
                                    .frame(height: 22)
                                    .background(modeChanged ? DesktopPalette.accent.opacity(0.18) : .clear, in: Capsule())
                                    .overlay(Capsule().strokeBorder(DesktopPalette.line))
                            }
                            .buttonStyle(.plain)
                            .help(shortcuts.help("Change mode", .composerCycleMode))
                            .accessibilityLabel("Mode, \(SessionRequests.permissionModeLabel(mode))")
                        }
                        if !session.sharedHolders.isEmpty {
                            HolderPicker(model: model, session: session)
                        }
                        if draft.stash != nil {
                            Button {
                                if draft.isEmpty { draft.toggleStash() }
                                focused = true
                            } label: {
                                Label("Stashed", systemImage: "tray.and.arrow.down")
                                    .font(.system(size: DesktopType.meta, weight: .medium))
                                    .foregroundStyle(DesktopPalette.text2)
                                    .padding(.horizontal, 8)
                                    .frame(height: 22)
                                    .overlay(Capsule().strokeBorder(DesktopPalette.line))
                            }
                            .buttonStyle(.plain)
                            .disabled(!draft.isEmpty)
                            .help(draft.isEmpty ? shortcuts.help("Bring back the stashed draft", .composerStash) : "The stashed draft comes back after you send")
                            .accessibilityLabel(draft.isEmpty ? "Bring back the stashed draft" : "A draft is stashed; it comes back after you send")
                        }
                        Spacer()
                        if session.isWorking && hasText && !draftIsCommand {
                            Text(hint)
                                .font(.system(size: DesktopType.caption))
                                .foregroundStyle(DesktopPalette.text2)
                                .padding(.trailing, DesktopSpace.xs)
                        }
                        if showsSteerNow {
                            Button("Steer now") { pane.submit(busyDelivery: FollowUpAction.steer.rawValue) }
                                .controlSize(.small)
                                .help(shortcuts.help("Change the turn in progress", .sendAlternate))
                        }
                        Button(action: primaryAction) {
                            Image(systemName: primarySymbol)
                                .font(.system(size: primaryIsStop ? 10 : 14, weight: .semibold))
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
                    .padding(.horizontal, DesktopSpace.s)
                    .padding(.top, 6)
                    .padding(.bottom, DesktopSpace.s)
                }
                .floatingGlass(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.sheet))
                .scrimText("composer")
                .overlay(RoundedRectangle(cornerRadius: DesktopRadius.sheet).strokeBorder(dropTargeted ? DesktopPalette.accent : DesktopPalette.line, lineWidth: dropTargeted ? 2 : 1))
                .shadow(color: .black.opacity(0.08), radius: 12, y: 6)
                .onDrop(of: [.fileURL, .image], isTargeted: Binding(get: { dropTargeted }, set: { dropTargeted = $0 && session.capabilities.attach })) { providers in
                    guard session.capabilities.attach else { return false }
                    Task {
                        if let pasted = await ComposerAttachments.pasted(from: providers) { add(pasted) }
                    }
                    return true
                }
            }
            if let error = model.operationError {
                Text(error)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.failed)
                    .scrimText("composer-error")
                    .padding(.top, 6)
            } else if model.pendingForks.contains(session.id) {
                HStack(spacing: DesktopSpace.s) {
                    Text("Codex may still be creating a fork of this session. Check the sidebar before forking again.")
                        .foregroundStyle(DesktopPalette.text2)
                    Button("Fork again") { pane.forkAgain(session) }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.accentText)
                        .help("Start another fork instead of waiting for this one")
                }
                .font(.system(size: DesktopType.meta))
                .scrimText("composer-fork")
                .padding(.top, 6)
            }
        }
        .frame(maxWidth: 700)
        .padding(.horizontal, DesktopSpace.xxl)
        .padding(.top, 40)
        .padding(.bottom, DesktopSpace.xl)
        .frame(maxWidth: .infinity)
        .backdropFade(DesktopPalette.window, paneID: "content", solidFrom: 0.38)
        .popover(isPresented: $showingSent, arrowEdge: .top) {
            SentPromptsList(prompts: draft.sent, canFork: SessionOperationAvailability.fork(session)) { prompt in
                draft.restore(prompt.text)
                showingSent = false
                focused = true
            } fork: { prompt in
                showingSent = false
                pane.fork(session, before: prompt)
            }
        }
        .popover(isPresented: $searchingSent, arrowEdge: .top) {
            PromptSearchPanel(model: model, sessionID: session.id) { text in
                draft.restore(text)
                searchingSent = false
                focused = true
            }
        }
        .onAppear { focused = true }
        .task(id: session.id) { await model.loadPrompts(for: session.id) }
    }

    private var primaryIsStop: Bool { SessionPane.stopAvailable(session, removed: false, draftEmpty: draft.isEmpty) }
    private var draftIsCommand: Bool {
        draft.attachments.isEmpty && (SlashInput(draft.text, available: SlashCommand.allCases) != .message || !slashSuggestions.isEmpty)
    }
    private var showsSteerNow: Bool { session.isWorking && canSteer && hasText && !draftIsCommand && resolvedAction(alternate: false) == .queue }
    private var slashSuggestions: [SlashCommandMenu.Suggestion] {
        guard draft.attachments.isEmpty, dismissedSlash != draft.text else { return [] }
        if let query = SlashCommand.modelQuery(for: draft.text), session.capabilities.model {
            let options = pane.detail?.models ?? []
            if options.contains(where: { $0.id == query }) { return [] }
            return options
                .filter { query.isEmpty || $0.id.localizedCaseInsensitiveContains(query) || $0.displayName.localizedCaseInsensitiveContains(query) }
                .map { SlashCommandMenu.Suggestion(id: "model:" + $0.id, title: $0.displayName, detail: $0.id == pane.detail?.model ? "Current" : $0.id == pane.detail?.nextTurnModel ? "From next turn" : ($0.description ?? $0.id), insertion: "/model " + $0.id) }
        }
        let available = SlashCommand.available(capabilities: session.capabilities, working: session.isWorking, waitingOnYou: session.isWaitingOnYou)
        return SlashCommand.suggestions(for: draft.text, available: available).map {
            SlashCommandMenu.Suggestion(id: $0.token, title: $0.token, detail: $0.summary, insertion: $0.token + ($0.needsArgument ? " " : ""))
        }
    }

    private var permissionMode: String? {
        guard session.capabilities.permissionMode, !session.isReadOnly else { return nil }
        return pane.requests?.permissionMode
    }

    private func perform(_ command: ComposerCommand) -> Bool {
        switch command {
        case .sendNow:
            if hasText {
                if session.isWorking && canSteer { pane.submit(busyDelivery: FollowUpAction.steer.rawValue) } else { submit(alternate: false) }
            }
            return true
        case .cycleMode:
            if permissionMode != nil || session.surface == "codex" {
                cycleMode()
                return true
            }
            return false
        case .interrupt:
            if !slashSuggestions.isEmpty {
                dismissedSlash = draft.text
                draft.escapeHandled()
                return true
            }
            if session.isWorking && session.capabilities.interrupt && !session.isReadOnly {
                pane.interrupt()
                draft.escapeHandled()
                return true
            }
            if draft.escapePressed(), !draft.clearIntoHistory() { showingSent = true }
            return true
        case .stash:
            return draft.toggleStash()
        case .searchHistory:
            searchingSent = true
            return true
        case .newline, .deleteToLineStart, .deleteWordBackward, .paste:
            return false
        }
    }

    private func olderPrompt() -> Bool {
        guard let text = draft.history.older(from: draft.text) else { return draft.history.isBrowsing }
        draft.text = text
        return true
    }

    private func newerPrompt() -> Bool {
        guard draft.history.isBrowsing, let text = draft.history.newer() else { return false }
        draft.text = text
        return true
    }

    private func cycleMode() {
        if let mode = permissionMode {
            Task { await pane.setPermissionMode(SessionRequests.cycled(from: mode)) }
        } else if session.surface == "codex" {
            var settings = model.turnSettings(for: session.id)
            settings.mode = TurnSettings.Mode.cycled(from: settings.mode)
            model.setTurnSettings(settings, for: session.id)
        }
        withAnimation(.easeOut(duration: 0.15)) { modeChanged = true }
        Task {
            try? await Task.sleep(for: .seconds(1.2))
            withAnimation(.easeOut(duration: 0.4)) { modeChanged = false }
        }
    }

    private func takeBackQueued() -> Bool {
        let queued = model.queuedItems(for: session.id)
        guard !queued.isEmpty else { return false }
        model.takeBackQueued(queued, sessionID: session.id)
        return true
    }

    private func chooseAttachments() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        panel.prompt = "Attach"
        guard panel.runModal() == .OK else { return }
        attach(panel.urls)
    }

    private func attach(_ urls: [URL]) {
        var pasted = PastedAttachments()
        for url in urls { pasted.add(url.dataRepresentation, type: UTType.fileURL.identifier) }
        add(pasted)
    }

    private func add(_ pasted: PastedAttachments) {
        let added = ComposerAttachments.adding(pasted.attachments, to: draft.attachments)
        draft.attachments = added.attachments
        model.operationError = (pasted.problem ?? added.problem)?.message
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
        if session.isWaitingOnYou { return "Sends after you answer" }
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
        if let first = slashSuggestions.first, String(draft.text.drop { $0.isWhitespace }) != first.insertion {
            draft.text = first.insertion
            return
        }
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
                .foregroundStyle(DesktopPalette.attention)
            VStack(alignment: .leading, spacing: 3) {
                Text("Not delivered · \(problem.reasonText)")
                    .font(.system(size: DesktopType.body, weight: .medium))
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
        .font(.system(size: DesktopType.body))
        .padding(.horizontal, DesktopSpace.m)
        .padding(.vertical, 9)
        .background(DesktopPalette.attentionBackground, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(DesktopPalette.attentionLine))
    }
}

struct QueueDock: View {
    @ObservedObject var model: AgenthailModel
    let items: [QueueState]
    let target: SessionState
    let canSteer: Bool

    var body: some View {
        VStack(spacing: 0) {
            ForEach(items) { item in
                let envelope = PeerEnvelope(item.message)
                HStack(spacing: 10) {
                    Image(systemName: "text.line.last.and.arrowtriangle.forward")
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.text2)
                    VStack(alignment: .leading, spacing: 2) {
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
                        Text(QueueFacts.summary(item, target: target, attention: model.snapshot?.attention ?? [], age: relativeAge(item.queuedAt)))
                            .font(.system(size: DesktopType.caption))
                            .foregroundStyle(DesktopPalette.text2)
                            .lineLimit(1)
                    }
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
                        Button("Edit") { model.takeBackQueued([item], sessionID: item.sessionId) }
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
                .font(.system(size: DesktopType.body))
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
            }
        }
        .padding(.top, DesktopSpace.xs)
        .padding(.bottom, DesktopSpace.xs)
        .background(DesktopPalette.dock, in: UnevenRoundedRectangle(topLeadingRadius: 14, topTrailingRadius: 14))
        .overlay(UnevenRoundedRectangle(topLeadingRadius: 14, topTrailingRadius: 14).strokeBorder(DesktopPalette.line))
    }
}

struct AttachmentDock: View {
    @Binding var attachments: [ComposerAttachment]
    let roundedTop: Bool

    var body: some View {
        let shape = UnevenRoundedRectangle(topLeadingRadius: roundedTop ? 14 : 0, topTrailingRadius: roundedTop ? 14 : 0)
        VStack(spacing: 0) {
            ForEach(attachments) { attachment in
                HStack(spacing: 10) {
                    AttachmentThumbnail(attachment: attachment)
                    HStack(spacing: 6) {
                        Text(attachment.name)
                            .foregroundStyle(DesktopPalette.text)
                        Text(ByteCountFormatter.string(fromByteCount: Int64(attachment.data.count), countStyle: .file))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    Button {
                        attachments.removeAll { $0.id == attachment.id }
                    } label: {
                        Image(systemName: "xmark")
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.text2)
                    .accessibilityLabel("Remove \(attachment.name)")
                }
                .font(.system(size: DesktopType.body))
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
            }
        }
        .padding(.vertical, DesktopSpace.xs)
        .background(DesktopPalette.dock, in: shape)
        .overlay(shape.strokeBorder(DesktopPalette.line))
    }
}

struct AttachmentThumbnail: View {
    let attachment: ComposerAttachment

    var body: some View {
        if attachment.isImage, let image = NSImage(data: attachment.data) {
            Image(nsImage: image)
                .resizable()
                .scaledToFill()
                .frame(width: 28, height: 28)
                .clipShape(RoundedRectangle(cornerRadius: DesktopRadius.control))
                .accessibilityHidden(true)
        } else {
            Image(systemName: "doc")
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.text2)
                .frame(width: 28, height: 28)
        }
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
            HStack(spacing: DesktopSpace.l) {
                ForEach(InspectorTab.allCases) { candidate in
                    Button(candidate.rawValue) { tab = candidate }
                        .buttonStyle(.plain)
                        .fontWeight(tab == candidate ? .semibold : .regular)
                        .foregroundStyle(tab == candidate ? DesktopPalette.text : DesktopPalette.text2)
                        .accessibilityAddTraits(tab == candidate ? .isSelected : [])
                }
                Spacer()
            }
            .font(.system(size: DesktopType.body))
            .padding(.horizontal, DesktopSpace.l)
            .frame(height: 52)
            .scrimText("inspector-tabs")
            .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
            if let session = pane.displayedSession {
                ScrollView {
                    Group {
                        switch tab {
                        case .details: DetailsTab(model: model, pane: pane, session: session)
                        case .delivery: DeliveryTab(model: model, session: session)
                        }
                    }
                    .font(.system(size: DesktopType.body))
                    .padding(DesktopSpace.l)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .scrimText("inspector-content")
                }
            } else {
                Spacer()
            }
        }
        .glassPane("inspector", .sidebar, fallback: DesktopPalette.side)
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
        return model.modelCatalog[session.modelCatalogKey] ?? []
    }

    private var catalogNeeded: String? {
        guard session.capabilities.model, pane.modelCatalogNeeded, pane.detail?.models?.isEmpty ?? true else { return nil }
        return session.surface
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.l) {
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
                        .padding(.top, DesktopSpace.xs)
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
                if let configRoot = session.claudeConfigRoot {
                    GridRow {
                        Text("Config folder").foregroundStyle(DesktopPalette.text2)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(configRoot.name)
                            if let account = configRoot.signedInAs, !account.isEmpty {
                                Text(account)
                                    .foregroundStyle(DesktopPalette.text2)
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                            }
                        }
                        .font(.system(size: DesktopType.body))
                        .textSelection(.enabled)
                    }
                    .help(configRoot.summary + "\n" + configRoot.root)
                }
                if let detail = pane.detail {
                    detailRow("Exchanges", SessionFacts.exchangeLabel(detail.exchanges.count))
                }
                if let background = session.backgroundTasks {
                    detailRow("Background", ([background.summary] + (background.tasks ?? []).map(\.line) + [background.sourceNote].compactMap { $0 }).joined(separator: "\n"))
                }
                let modelName = pane.detail?.model
                if canControl && session.capabilities.model, case let options = modelOptions, !options.isEmpty {
                    GridRow {
                        Text("Model").foregroundStyle(DesktopPalette.text2)
                        Menu(MetadataLabels.modelMenuTitle(current: options.first { $0.id == modelName }?.displayName ?? modelName, next: options.first { $0.id == pane.detail?.nextTurnModel }?.displayName ?? pane.detail?.nextTurnModel)) {
                            ForEach(options) { option in
                                Button {
                                    pane.changeModel(to: option.id)
                                } label: {
                                    if option.id == modelName { Label(option.displayName, systemImage: "checkmark") } else if option.id == pane.detail?.nextTurnModel { Label(option.displayName + " (from next turn)", systemImage: "clock") } else { Text(option.displayName) }
                                }
                            }
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                        .disabled(pane.controlPending)
                    }
                } else if let modelLabel = pane.detail?.modelLabel {
                    detailRow("Model", modelLabel)
                }
                if canControl && session.capabilities.permissionMode, let current = pane.requests?.permissionMode {
                    GridRow {
                        Text("Permissions").foregroundStyle(DesktopPalette.text2)
                        Menu(SessionRequests.permissionModeLabel(current)) {
                            ForEach(SessionRequests.permissionModes, id: \.self) { mode in
                                Button {
                                    Task { await pane.setPermissionMode(mode) }
                                } label: {
                                    if mode == current { Label(SessionRequests.permissionModeLabel(mode), systemImage: "checkmark") } else { Text(SessionRequests.permissionModeLabel(mode)) }
                                }
                            }
                        }
                        .menuStyle(.borderlessButton)
                        .fixedSize()
                    }
                }
                if let project = session.project?.displayName { detailRow("Project", project) }
                if let branch = session.checkout?.branch ?? session.checkout?.detachedHead { detailRow("Branch", branch, monospaced: true) }
                if let path = session.checkout?.path ?? session.cwd { detailRow("Checkout", (path as NSString).abbreviatingWithTildeInPath, monospaced: true) }
            }
            if canControl && session.capabilities.compact {
                Button("Compact context") { pane.compactContext() }
                    .disabled(pane.controlPending)
                    .help("Ask the agent to compact its context")
            }
            if canControl, let stop = session.limitResume {
                HStack {
                    Button("Resume now") { pane.resumeAfterLimit(true) }
                        .help("Send the session its queued messages, or ask it to continue, now")
                    if stop.isWaiting {
                        Button("Don't resume") { pane.resumeAfterLimit(false) }
                            .help("Keep the session stopped when its usage limit resets")
                    }
                }
                .disabled(pane.controlPending)
            }
            GoalSection(model: model, pane: pane, session: session)
            SessionOperationsSection(model: model, pane: pane, session: session)
            SubagentsSection(model: model, pane: pane, session: session)
            ClaudeObservationsSection(detail: pane.detail)
        }
        .task(id: catalogNeeded) {
            if catalogNeeded != nil { await model.loadModelCatalog(for: session) }
        }
        .task(id: SubagentFetchKey(session, revision: model.fetchedSubagents.revision(session.id))) { await model.loadSubagents(of: session) }
    }

    private var canControl: Bool { !session.isReadOnly && pane.removedSession == nil }

    @ViewBuilder
    private func detailRow(_ label: String, _ value: String, monospaced: Bool = false) -> some View {
        GridRow {
            Text(label).foregroundStyle(DesktopPalette.text2)
            Text(value)
                .font(monospaced ? .system(size: DesktopType.meta, design: .monospaced) : .system(size: DesktopType.body))
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
    }
}

struct DeliveryTab: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState

    var body: some View {
        let queued = QueueFacts.undelivered(model.snapshot?.queue ?? [], for: session.id)
        let history = model.snapshot?.history.filter { $0.sessionId == session.id || $0.sourceSessionId == session.id } ?? []
        let problems = model.deliveryProblems(for: session.id)
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            if !problems.isEmpty {
                SidebarCaption("Not delivered").padding(.horizontal, -8)
                ForEach(problems) { problem in
                    deliveryCard(title: problem.sessionId == session.id ? "To this session" : "To \(model.snapshot?.sessions.first { $0.id == problem.sessionId }?.title ?? "another session")", status: problem.reasonText, text: PeerEnvelope(problem.message).body, problem: true)
                }
            }
            if !queued.isEmpty {
                SidebarCaption("Undelivered").padding(.horizontal, -8)
                ForEach(queued) { item in
                    VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                        deliveryCard(title: item.target, status: QueueFacts.statusLabel(item.status), text: item.message, detail: QueueFacts.summary(item, target: session, attention: model.snapshot?.attention ?? [], age: relativeAge(item.queuedAt)), problem: item.status == "dead")
                        HStack { QueueItemActions(model: model, item: item) }
                            .controlSize(.small)
                    }
                }
            }
            if history.isEmpty && queued.isEmpty && problems.isEmpty {
                Text("No deliveries for this session yet.")
                    .foregroundStyle(DesktopPalette.text2)
            } else if !history.isEmpty {
                SidebarCaption("Recent").padding(.horizontal, -8)
                ForEach(history) { entry in
                    AuditEventRow(entry: entry)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 9)
                        .background(DesktopPalette.window, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
                        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(DesktopPalette.line2))
                }
            }
        }
    }

    private func deliveryCard(title: String, status: String, text: String, detail: String? = nil, problem: Bool) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text(title)
                Spacer()
                Text(status)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(problem ? DesktopPalette.attention : DesktopPalette.text2)
            }
            Text(text)
                .lineLimit(1)
                .foregroundStyle(DesktopPalette.text2)
            if let detail, !detail.isEmpty {
                Text(detail)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(problem ? DesktopPalette.attention : DesktopPalette.text2)
                    .lineLimit(3)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 9)
        .background(problem ? DesktopPalette.attentionBackground : DesktopPalette.window, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
        .overlay(RoundedRectangle(cornerRadius: DesktopRadius.card).strokeBorder(problem ? DesktopPalette.attentionLine : DesktopPalette.line2))
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
