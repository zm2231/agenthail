import SwiftUI
import UIKit
import ImageIO

struct AgenthailIOSRoot: View {
    @ObservedObject var model: AgenthailIOSModel

    var body: some View {
        AppearanceScope(appearance: model.appearance) {
            if model.isPaired {
                MainTabs(model: model)
            } else {
                PairingScreen(model: model)
            }
        }
        .alert("Agenthail", isPresented: Binding(get: { model.operationError != nil }, set: { if !$0 { model.operationError = nil } })) {
            Button("OK") { model.operationError = nil }
        } message: {
            Text(model.operationError ?? "")
        }
        .confirmationDialog("\(model.forgetCandidate?.name ?? "This computer") could not be reached", isPresented: Binding(get: { model.forgetCandidate != nil }, set: { if !$0 { model.forgetCandidate = nil } }), titleVisibility: .visible) {
            Button("Forget it on this iPhone", role: .destructive) { if let host = model.forgetCandidate { model.forgetHost(host.id) } }
            Button("Keep connection", role: .cancel) { model.forgetCandidate = nil }
        } message: {
            Text("Forget the saved connection only if this computer is gone. Your other computers stay paired.")
        }
        .confirmationDialog(model.pairingConfirmationTitle, isPresented: $model.showPairingConfirmation, titleVisibility: .visible) {
            Button(model.pairingConfirmationAction) { model.confirmPairing() }
            Button("Cancel", role: .cancel) { model.cancelPairing() }
        } message: {
            Text(model.pairingConfirmationMessage)
        }
    }
}

struct PairingScreen: View {
    @ObservedObject var model: AgenthailIOSModel

    var body: some View {
        NavigationStack { PairingForm(model: model) }
    }
}

struct PairingForm: View {
    @ObservedObject var model: AgenthailIOSModel
    var adding = false
    @State private var scanning = false
    @State private var pastedURL = ""
    @Environment(\.dismiss) private var dismiss

    var body: some View {
            ScrollView {
            VStack(spacing: 24) {
                ZStack {
                    RoundedRectangle(cornerRadius: 28).fill(PhonePalette.accent)
                    Text("A").font(.system(size: 58, weight: .black)).foregroundStyle(.black)
                }
                .frame(width: 112, height: 112)
                VStack(spacing: 8) {
                    Text(adding ? "Add a computer" : "Connect to your agents").font(.largeTitle.bold()).multilineTextAlignment(.center)
                    Text("On a computer running Agenthail, run agenthail devices pair, or open Settings in its web dashboard or Mac app, and show a pairing code.")
                        .foregroundStyle(PhonePalette.muted).multilineTextAlignment(.center)
                }
                .padding(.horizontal)
                Button {
                    scanning = true
                } label: {
                    Label("Scan pairing code", systemImage: "qrcode.viewfinder").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent).tint(PhonePalette.accent).controlSize(.large)
                .padding(.horizontal, 28)
                TextField("Or paste a pairing link", text: $pastedURL)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .textFieldStyle(.roundedBorder)
                    .padding(.horizontal, 28)
                    .onSubmit {
                        if let url = URL(string: pastedURL) { model.handlePairingURL(url) }
                    }
                Button("Connect with pairing link") {
                    if let url = URL(string: pastedURL.trimmingCharacters(in: .whitespacesAndNewlines)) { model.handlePairingURL(url) }
                }.disabled(pastedURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.pairing)
                if model.pairing { ProgressView("Pairing securely") }
                Spacer()
                Text("Your conversations stay between this phone and your computer over Tailscale.")
                    .font(.footnote).foregroundStyle(PhonePalette.muted).multilineTextAlignment(.center).padding(.horizontal, 30)
            }
            .padding(.vertical)
            .frame(maxWidth: 560)
            .frame(maxWidth: .infinity)
            }
            .scrollDismissesKeyboard(.interactively)
            .sheet(isPresented: $scanning) {
                NavigationStack {
                    QRCodeScanner { value in
                        scanning = false
                        if let url = URL(string: value) { model.handlePairingURL(url) }
                    }
                    .ignoresSafeArea()
                    .navigationTitle("Scan Agenthail code")
                    .navigationBarTitleDisplayMode(.inline)
                    .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Cancel") { scanning = false } } }
                }
            }
            .onChange(of: model.completedPairings) {
                if adding { dismiss() }
            }
    }
}

struct MainTabs: View {
    @ObservedObject var model: AgenthailIOSModel
    @State private var selection = 0

    var body: some View {
        TabView(selection: $selection) {
            ConversationFlow(model: model)
                .solidTabBar()
                .tabItem { Label("Sessions", systemImage: "bubble.left.and.bubble.right") }
                .tag(0)
            NavigationStack { QueueListView(model: model) }
                .solidTabBar()
                .tabItem { Label("Inbox", systemImage: "tray") }
                .tag(1)
            NavigationStack { SettingsView(model: model) }
                .solidTabBar()
                .tabItem { Label("Settings", systemImage: "gearshape") }
                .tag(2)
        }
        .onChange(of: model.requestedSessionID) { _, sessionID in
            if sessionID != nil { selection = 0 }
        }
        .onReceive(NotificationCenter.default.publisher(for: .agenthailNotificationOpened)) { notification in
            guard let sessionID = notification.object as? String else { return }
            model.openSession(sessionID)
            selection = 0
        }
    }
}

struct ConversationFlow: View {
    @ObservedObject var model: AgenthailIOSModel
    @Environment(\.horizontalSizeClass) private var sizeClass
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var path: [String] = []
    @State private var wideSelection: String?

    init(model: AgenthailIOSModel, initialSessionID: String? = nil) {
        self.model = model
        _wideSelection = State(initialValue: initialSessionID)
        _path = State(initialValue: initialSessionID.map { [$0] } ?? [])
    }

    var body: some View {
        Group {
            if sizeClass == .regular && !dynamicTypeSize.isAccessibilitySize {
                NavigationSplitView {
                    ConversationListView(model: model, selectedID: wideSelection) { wideSelection = $0; path = [$0] }
                } detail: {
                    if let wideSelection { SessionRouteView(model: model, sessionID: wideSelection) }
                    else { ContentUnavailableView("Choose a session", systemImage: "desktopcomputer", description: Text("Follow work on your computers, review results, or give the next instruction.")).phoneBackdrop(.pane) }
                }
            } else {
                NavigationStack(path: $path) {
                    ConversationListView(model: model) { path.append($0); wideSelection = $0 }
                        .navigationDestination(for: String.self) { sessionID in
                            SessionRouteView(model: model, sessionID: sessionID)
                        }
                }
            }
        }
        .onChange(of: model.requestedSessionID) { _, sessionID in
            guard let sessionID else { return }
            path = [sessionID]
            wideSelection = sessionID
            model.requestedSessionID = nil
        }
    }
}

struct SessionRouteView: View {
    @ObservedObject var model: AgenthailIOSModel
    let sessionID: String

    private var session: SessionState? {
        if let value = model.snapshot?.sessions.first(where: { $0.id == sessionID }) { return value }
        if let value = model.searchResults.first(where: { $0.id == sessionID })?.session { return value }
        if let value = model.fetchedSubagents.session(sessionID) { return value }
        guard let detail = model.selectedDetail, detail.session.id == sessionID else { return nil }
        var unlisted = SessionState(id: sessionID, surface: detail.session.surface, name: detail.session.name, alias: detail.alias,
                                    status: detail.session.status, lastActive: detail.session.lastActive, queueCount: 0, open: false,
                                    current: false, currentReason: nil, capabilities: detail.capabilities, readOnly: detail.readOnly,
                                    readOnlyReason: detail.readOnlyReason)
        unlisted.host = model.snapshot?.daemon.pairedHost(owning: sessionID)
        return unlisted
    }
    var body: some View {
        Group {
            if let session { SessionScreen(model: model, session: session) }
            else if let error = model.sessionError {
                ContentUnavailableView {
                    Label("Conversation unavailable", systemImage: "exclamationmark.bubble")
                } description: { Text(error) } actions: {
                    Button("Retry") { Task { await model.loadSession(sessionID) } }
                }
            } else { ProgressView("Opening conversation") }
        }
        .task(id: sessionID) { if session == nil { await model.loadSession(sessionID) } }
    }
}

struct ConversationListView: View {
    @ObservedObject var model: AgenthailIOSModel
    var selectedID: String? = nil
    let openSession: (String) -> Void
    @State private var scope = SessionFilter.recent
    @State private var showingNewSession = false
    @State private var search = ""
    @State private var collapsedProjects: Set<String> = []
    @State private var expandedFamilies: Set<String> = []
    @State private var projectSheet: ProjectSheet?
    @State private var projectConfirmation: ProjectConfirmation?
    @State private var newSessionPlace: NewSessionPlace?
    @StateObject private var usage = UsageFeed()
    @ScaledMetric(relativeTo: .body) private var titleInset: CGFloat = 53

    private var tree: SessionTree { model.sessionTree(scope: scope, search: search) }

    private func projectsWithoutSessions(_ tree: SessionTree) -> [ListedProject] {
        guard (scope == .recent || scope == .all) && search.isEmpty else { return [] }
        let listed = Set(tree.projects.compactMap(\.identity?.id))
        return model.projects.filter { $0.value.state.archived != true && !listed.contains($0.key) }.sorted { $0.key < $1.key }.map(\.value)
    }

    var body: some View {
        let tree = tree
        let families = tree.projects.flatMap { $0.checkouts.flatMap(\.families) }
        List {
            SessionScopeChips(scope: $scope, runningCount: tree.counts[.running] ?? 0)
                .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 4, trailing: 0))
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            UsageMetersRow(feed: usage, sessions: model.snapshot?.sessions ?? [], open: openSession)
            if let error = model.connectionError {
                Label(error, systemImage: "wifi.exclamationmark").font(.footnote).foregroundStyle(PhonePalette.muted)
                    .listRowBackground(GlassRowBackground(selected: false))
            } else if model.reconnecting {
                Label("Reconnecting to your computer", systemImage: "desktopcomputer").font(.footnote).foregroundStyle(PhonePalette.muted)
                    .listRowBackground(GlassRowBackground(selected: false))
            }
            if !tree.needsYou.isEmpty {
                Section {
                    ForEach(tree.needsYou) { session in
                        sessionButton(session, reason: tree.attention[session.id]?.reason, identifierPrefix: "needs-you-")
                    }
                } header: {
                    Label {
                        Text("Needs you · \(tree.needsYou.count)")
                    } icon: {
                        Image(systemName: "exclamationmark.bubble.fill").foregroundStyle(PhonePalette.attention)
                    }
                    .font(.subheadline.weight(.semibold)).foregroundStyle(.primary).textCase(nil).glassLabel()
                    .accessibilityLabel(tree.needsYou.count == 1 ? "Needs you, 1 session" : "Needs you, \(tree.needsYou.count) sessions")
                }
            }
            ForEach(tree.projects) { project in
                let collapsed = collapsedProjects.contains(project.id)
                Section {
                    ForEach(project.checkouts.flatMap(\.families).filter { !collapsed || project.pinned.contains($0.id) }) { family in familyRows(family) }
                } header: {
                    Button {
                        if !collapsedProjects.insert(project.id).inserted { collapsedProjects.remove(project.id) }
                    } label: {
                        HStack(spacing: 8) {
                            Image(systemName: project.identity == nil ? "tray" : project.archived ? "archivebox" : "folder")
                            Text(project.name)
                                .font(.subheadline.weight(.semibold))
                                .lineLimit(1)
                            if let host = project.hostLabel, model.showsHostNames {
                                IOSHostBadge(name: host)
                            }
                            Spacer(minLength: 0)
                            Text("\(project.listedSessionCount)")
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(PhonePalette.muted)
                            Image(systemName: collapsed ? "chevron.right" : "chevron.down")
                                .font(.caption.weight(.semibold))
                        }
                        .glassLabel()
                        .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.primary)
                    .textCase(nil)
                    .contextMenu {
                        if let identity = project.identity {
                            Button(project.archived ? "Unarchive Project" : "Archive Project", systemImage: "archivebox") {
                                Task { await model.setProjectArchived(identity.id, !project.archived) }
                            }
                            if let listed = model.projects[identity.id] {
                                IOSProjectMenuItems(model: model, project: listed, sheet: $projectSheet, confirmation: $projectConfirmation, place: $newSessionPlace)
                            }
                        }
                    }
                    .accessibilityLabel(project.hostLabel.map { "Project \(project.name) on \($0)" } ?? "Project \(project.name)")
                    .accessibilityValue(project.sessionCountLabel + (collapsed ? ", collapsed" : ", expanded"))
                    .accessibilityIdentifier("project-" + project.id)
                }
            }
            let empty = projectsWithoutSessions(tree)
            if !empty.isEmpty {
                Section("Projects without sessions") {
                    ForEach(empty, id: \.state.id) { listed in
                        Menu {
                            IOSProjectMenuItems(model: model, project: listed, sheet: $projectSheet, confirmation: $projectConfirmation, place: $newSessionPlace)
                        } label: {
                            Label(listed.state.displayName, systemImage: "folder").foregroundStyle(.primary)
                        }
                    }
                }
            }
            if families.isEmpty && empty.isEmpty && !model.searching {
                ContentUnavailableView {
                    Label(scope == .running ? "No agents working" : scope == .archived ? "Nothing archived" : "No sessions here", systemImage: "bubble.left.and.bubble.right")
                } description: {
                    Text(search.isEmpty ? "Start a session, or browse All to continue earlier work." : "Try another title or search All sessions.")
                } actions: {
                    Button("New session", systemImage: "plus") { showingNewSession = true }
                }
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            }
            if search.count >= 3 {
                Section("From saved history") {
                    if model.searching { ProgressView("Searching") }
                    if let error = model.searchError { Text(error).font(.footnote).foregroundStyle(PhonePalette.muted) }
                    ForEach(model.searchResults.filter { result in !families.contains(where: { $0.contains(result.id) }) }) { result in
                        sessionButton(result.session, snippet: result.snippet)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .listSectionSpacing(14)
        .phoneBackdrop(.list)
        .navigationTitle("Sessions")
        .searchable(text: $search, prompt: "Search sessions")
        .task(id: search) { await model.searchSessions(search) }
        .task(id: model.usageReaders.map(ObjectIdentifier.init)) { await usage.run(model.usageReaders) }
        .toolbar {
            if model.isPaired {
                ToolbarItem(placement: .topBarLeading) { VoiceToolbarButton(model: model) }
            }
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button("New session", systemImage: "square.and.pencil") {
                        model.creationError = nil
                        showingNewSession = true
                    }.accessibilityIdentifier("new-session")
                    Button("Add project", systemImage: "folder.badge.plus") { projectSheet = .add(host: nil, folder: "") }
                        .accessibilityIdentifier("add-project")
                } label: {
                    Image(systemName: "plus")
                } primaryAction: {
                    model.creationError = nil
                    showingNewSession = true
                }
                .accessibilityLabel("New")
                .accessibilityIdentifier("new-menu")
            }
        }
        .sheet(isPresented: $showingNewSession) { NewSessionSheet(model: model) }
        .sheet(item: $newSessionPlace) { place in NewSessionSheet(model: model, place: place) }
        .sheet(item: $projectSheet) { sheet in IOSProjectSheetView(model: model, sheet: sheet) }
        .alert(projectConfirmation?.message ?? "", isPresented: Binding(get: { projectConfirmation != nil }, set: { if !$0 { projectConfirmation = nil } }), presenting: projectConfirmation) { confirmation in
            Button(confirmation.action, role: .destructive) { Task { model.settle(await confirmation.run()) } }
            Button("Cancel", role: .cancel) {}
        }
        .refreshable { await model.refresh(fresh: true) }
    }

    @ViewBuilder
    private func familyRows(_ family: SessionFamily) -> some View {
        let expanded = !family.members.isEmpty && (expandedFamilies.contains(family.id) || family.members.contains { $0.id == selectedID })
        sessionButton(family.root, family: family)
        if !family.members.isEmpty {
            Button { toggle(family) } label: {
                Label(expanded ? "Hide subagents" : "Show \(family.members.count == 1 ? "1 subagent" : "\(family.members.count) subagents")",
                      systemImage: expanded ? "chevron.down" : "chevron.right")
                    .font(.footnote)
                    .foregroundStyle(PhonePalette.muted)
                    .frame(maxWidth: .infinity, minHeight: 32, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.leading, titleInset)
            .listRowBackground(GlassRowBackground(selected: false))
            .accessibilityIdentifier("subagents-" + family.id)
        }
        if expanded {
            ForEach(family.members) { member in
                sessionButton(member.session, title: SessionFamilies.label(member.session))
                    .padding(.leading, CGFloat(member.depth) * 16)
            }
        }
    }

    private func toggle(_ family: SessionFamily) {
        if !expandedFamilies.insert(family.id).inserted { expandedFamilies.remove(family.id) }
    }

    private func sessionButton(_ session: SessionState, title: String? = nil, family: SessionFamily? = nil, snippet: String? = nil, reason: String? = nil, identifierPrefix: String = "session-") -> some View {
        let ring = SessionRing.kind(session, needsYou: tree.attention[session.id] != nil)
        return Button { openSession(session.id) } label: {
            HStack(alignment: .top, spacing: 12) {
                SessionAvatar(surface: session.surface, ring: ring)
                VStack(alignment: .leading, spacing: 5) {
                    IOSSessionRow(session: session, title: title, family: family, showsHost: model.showsHostNames)
                    if let reason {
                        Text(reason).font(.subheadline).foregroundStyle(PhonePalette.attention).lineLimit(2)
                    }
                    if let snippet, !snippet.isEmpty {
                        Text(snippet).font(.subheadline).foregroundStyle(PhonePalette.muted).lineLimit(2)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }.contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityValue(ring.spoken ?? "")
        .listRowBackground(GlassRowBackground(selected: selectedID == session.id))
        .swipeActions(edge: .trailing) {
            if snippet == nil {
                Button(session.archived == true ? "Unarchive" : "Archive", systemImage: "archivebox") {
                    Task { await model.setArchived(sessionID: session.id, session.archived != true) }
                }
            }
        }
        .accessibilityIdentifier(identifierPrefix + session.id)
    }
}

struct SessionScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    @EnvironmentObject private var voiceCalls: VoiceCallCoordinator
    let session: SessionState
    private var liveSession: SessionState { model.snapshot?.sessions.first { $0.id == session.id } ?? session }
    @State private var showingInfo = false
    @State private var showingVoice = false
    @State private var activityOnly = false
    @AppStorage(AgentFold.defaultsKey) private var expandAgents = false
    @State private var followingLatest = true
    @State private var atLatest = true
    @State private var overflows = false
    @State private var jumps = 0
    @State private var userScrolling = false
    @State private var position = ScrollPosition()

    private var displayTitle: String {
        session.subagent == nil ? SessionStyle.title(session) : SessionFamilies.title(session, in: model.snapshot?.sessions ?? [])
    }

    private var detail: SessionDetail? {
        model.selectedDetail?.session.id == session.id ? model.selectedDetail : nil
    }
    private var items: [TimelineItem] {
        let latest = detail?.timeline?.items ?? []
        let ids = Set(latest.map(\.id))
        return model.olderActivity.filter { !ids.contains($0.id) } + latest
    }

    static func showsActivity(timeline: SessionTimeline, itemCount: Int, activityCursor: Int64?, exchangeCount: Int) -> Bool {
        timeline.unavailableReason == nil && (itemCount > 0 || (activityCursor ?? 0) > 0 || exchangeCount == 0)
    }

    private var latestRowID: String? {
        guard let detail, let timeline = detail.timeline else { return nil }
        let items = items
        guard Self.showsActivity(timeline: timeline, itemCount: items.count, activityCursor: model.activityCursor, exchangeCount: detail.exchanges.count) else { return nil }
        return activityOnly ? items.last?.id : TimelineGroup.make(items, foldAgents: !expandAgents).last?.id
    }

    private func scrollToLatest() {
        if overflows, let id = latestRowID {
            position.scrollTo(id: id, anchor: .bottom)
        } else {
            position.scrollTo(edge: .bottom)
        }
    }

    private func settleAtLatest() async {
        for _ in 0..<5 {
            try? await Task.sleep(for: .milliseconds(50))
            guard followingLatest, !atLatest else { return }
            scrollToLatest()
        }
    }

    @ViewBuilder private var transcriptRows: some View {
        let connection = model.connectionState(for: session.id)
        if let error = connection.error {
            Label(error, systemImage: "wifi.exclamationmark").font(.callout).foregroundStyle(PhonePalette.muted)
        } else if connection.reconnecting {
            Label("Reconnecting live updates", systemImage: "arrow.triangle.2.circlepath").font(.callout).foregroundStyle(PhonePalette.muted)
        }
        if model.loadingSession && detail == nil { ProgressView("Loading conversation") }
        if let error = model.sessionError {
            ContentUnavailableView {
                Label("Could not refresh conversation", systemImage: "exclamationmark.bubble")
            } description: { Text(error) } actions: {
                Button("Retry") { Task { await model.loadSession(session.id) } }
            }
        }
        if let detail {
            if let warning = detail.transcriptWarning {
                Label(warning, systemImage: "exclamationmark.bubble").font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            if let timeline = detail.timeline,
               Self.showsActivity(timeline: timeline, itemCount: items.count, activityCursor: model.activityCursor, exchangeCount: detail.exchanges.count) {
                if (model.activityCursor ?? 0) > 0 {
                    Button { followingLatest = false; Task { await model.loadOlderActivity() } } label: {
                        if model.loadingOlderActivity { ProgressView("Loading older activity") }
                        else { Label("Load older activity", systemImage: "arrow.up") }
                    }.disabled(model.loadingOlderActivity).frame(minHeight: 44)
                }
                if let error = model.olderActivityError { Text(error).font(.footnote).foregroundStyle(PhonePalette.muted) }
                if timeline.truncated {
                    Label("Showing recent activity. Older activity or long output has been shortened.", systemImage: "text.badge.ellipsis")
                        .font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                if activityOnly {
                    ForEach(items) { IOSTimelineRow(item: $0, compactContext: false, bodyLoader: { item in await model.retainedSessionBody(for: item) }, attachmentLoader: { item in await model.sessionAttachment(for: item, sessionID: session.id) }).id($0.id) }
                } else {
                    ForEach(TimelineGroup.make(items, foldAgents: !expandAgents)) { group in
                        CompactActivityGroup(group: group, attachmentLoader: { item in await model.sessionAttachment(for: item, sessionID: session.id) }) { followingLatest = false }.id(group.id)
                    }
                }
                if items.isEmpty { ContentUnavailableView("Ready for your instruction", systemImage: "bubble.left", description: Text("Messages and agent activity will appear here.")) }
            } else {
                if let reason = detail.timeline?.unavailableReason {
                    Label(reason, systemImage: "info.circle").font(.footnote).foregroundStyle(PhonePalette.muted)
                } else {
                    Text("Message history. Detailed activity is not available for this session.").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                if detail.transcriptTruncated == true {
                    Text("Older or oversized messages were shortened.").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                ForEach(Array(detail.exchanges.enumerated()), id: \.offset) { _, exchange in
                    if !exchange.user.isEmpty { IOSMessage(label: exchange.promptLabel, text: exchange.user, timestamp: exchange.timestamp) }
                    if !exchange.assistant.isEmpty { IOSMessage(label: "Assistant", text: exchange.assistant, timestamp: exchange.timestamp) }
                }
                if detail.exchanges.isEmpty {
                    ContentUnavailableView("No messages yet", systemImage: "bubble.left", description: Text("Messages will appear here as the agent works."))
                }
            }
        }
    }

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                Group { transcriptRows }.transcriptScrimRow()
            }
            .environment(\.editMessage, detail.map { $0.capabilities.send && !$0.readOnly } == true ? { model.editAndResend($0) } : nil)
            .environment(\.revealRow) { position.scrollTo(y: $0) }
            .environment(\.transcriptRows, TranscriptRows(memory: model.rowMemory, session: session.id))
            .environment(\.toolCallStates, ToolCallStates(items: items, active: session.isWorking || session.isWaitingOnYou))
            .environment(\.expandAgentMessages, expandAgents)
            .frame(maxWidth: SessionStyle.readingWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 20)
            .padding(.vertical, 16)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .transcriptScrimContent()
            .id(jumps)
        }
        .scrollPosition($position)
        .accessibilityIdentifier("session-timeline")
        .phoneBackdrop(.transcript)
        .defaultScrollAnchor(.bottom, for: .initialOffset)
        .defaultScrollAnchor(followingLatest ? .bottom : nil, for: .sizeChanges)
        .defaultScrollAnchor(.top, for: .alignment)
        .scrollDismissesKeyboard(.interactively)
        .onScrollPhaseChange { _, phase in
#if DEBUG
            guard !ProcessInfo.processInfo.arguments.contains("--preview-session") || PreviewStream.follows else { return }
#endif
            if phase == .interacting {
                userScrolling = true
                followingLatest = false
            } else if phase == .idle && userScrolling {
                followingLatest = atLatest
                userScrolling = false
            }
        }
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentSize.height - geometry.visibleRect.maxY < 60
        } action: { _, value in
#if DEBUG
            guard !ProcessInfo.processInfo.arguments.contains("--preview-session") || PreviewStream.follows else { return }
#endif
            atLatest = value
        }
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentSize.height > geometry.visibleRect.height
        } action: { _, value in
            overflows = value
        }
        .refreshable { await model.refreshSession(session.id) }
        .task(id: "\(session.id)|\(session.waitingOn?.id ?? "")|\(session.capabilities.respond)|\(session.isReadOnly)") { await model.syncRequests(for: session) }
        .edgeBar(.top, spacing: 0) {
            if let detail { SessionSummary(detail: detail) { showingInfo = true } }
        }
        .edgeBar(.bottom) {
            VStack(spacing: 0) {
                if !atLatest && detail != nil {
                    Button {
                        followingLatest = true
                        jumps += 1
                        position.scrollTo(edge: .bottom)
                        Task { await settleAtLatest() }
                    } label: {
                        Image(systemName: "arrow.down")
                            .font(.body.weight(.semibold))
                            .frame(width: 44, height: 44)
                            .floatingGlass(in: Circle(), interactive: true)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Jump to latest")
                }
                if let detail, let waiting = detail.session.waitingOn, ElsewhereWaitCard.shows(waiting, readOnly: detail.readOnly, respond: detail.capabilities.respond) {
                    ElsewhereWaitCard(waiting: waiting, place: detail.waitingPlace ?? .elsewhere,
                                      surface: session.surface, hostName: session.runtime?.hostName, accent: AnyShapeStyle(PhonePalette.accentText),
                                      open: session.runtime?.focusable == true ? { model.focusSession(session) } : nil,
                                      restart: { model.stopAndResume(session.id) })
                        .id(waiting.id)
                        .padding(.horizontal)
                        .padding(.top, 6)
                } else if let detail, !detail.readOnly, let request = model.sessionRequests?.requests.first {
                    RequestCard(request: request, answering: model.answeringRequest != nil, accent: AnyShapeStyle(PhonePalette.accentText)) { decision in
                        Task { await model.respond(to: request, sessionID: session.id, decision: decision) }
                    }
                    .id(request.id)
                    .padding(.horizontal)
                    .padding(.top, 6)
                }
                if let detail {
                    if detail.readOnly || (!detail.capabilities.send && !detail.capabilities.steer) {
                        Label(detail.readOnlyReason.isEmpty ? "This session does not support messages." : detail.readOnlyReason, systemImage: "lock")
                            .font(.footnote).foregroundStyle(PhonePalette.muted)
                            .padding(.horizontal, 14).padding(.vertical, 10)
                            .floatingGlass(in: Capsule())
                            .padding()
                    } else {
                        if !liveSession.composerTurnNotice.isEmpty {
                            Label(liveSession.composerTurnNotice, systemImage: "terminal")
                                .font(.footnote).foregroundStyle(PhonePalette.muted)
                                .padding(.horizontal, 12).padding(.vertical, 7)
                                .floatingGlass(in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                                .padding(.horizontal).padding(.top, 6)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        IOSComposer(model: model, draft: model.draft, session: session, detail: detail)
                    }
                }
            }
        }
        .toolbar(.hidden, for: .tabBar)
        .navigationTitle(displayTitle)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 2) {
                    Text(displayTitle).font(.body.weight(.semibold)).lineLimit(1)
                    if let cwd = detail?.session.cwd, !cwd.isEmpty {
                        Text(URL(fileURLWithPath: cwd).lastPathComponent + " · " + (session.host?.name ?? "computer")).font(.caption2).foregroundStyle(PhonePalette.muted).lineLimit(1)
                    }
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button("Session details", systemImage: "info.circle") { showingInfo = true }
                    Picker("Transcript view", selection: $activityOnly) {
                        Text("Chat").tag(false)
                        Text("All events").tag(true)
                    }
                    Toggle("Expand agent messages", systemImage: "text.bubble", isOn: $expandAgents)
                    if model.canCallByVoice(session) {
                        Button("Call this session", systemImage: "phone.fill") {
                            voiceCalls.open(targetID: model.voiceTargetID(for: session))
                            showingVoice = true
                        }
                    }
                } label: { Image(systemName: "ellipsis") }
                .accessibilityLabel("Session menu")
                .disabled(detail == nil)
            }
        }
        .sheet(isPresented: $showingInfo) {
            if let liveDetail = model.selectedDetail, liveDetail.session.id == session.id {
                SessionInspector(model: model, session: session, initialDetail: liveDetail)
            }
        }
        .sheet(isPresented: $showingVoice) {
            if let call = voiceCalls.model {
                AgenthailVoiceOperatorSheet(model: call, rowMemory: model.rowMemory) { _ in }
            }
        }
        .task(id: session.id) {
            let startsAtOldest = ProcessInfo.processInfo.arguments.contains("--preview-reading-top")
            followingLatest = !startsAtOldest
            atLatest = !startsAtOldest
            await model.loadSession(session.id)
            if startsAtOldest, let first = items.first {
                position.scrollTo(id: first.id, anchor: .top)
            } else {
                scrollToLatest()
            }
        }
        .onChange(of: detail?.timeline?.items) { _, _ in
            if followingLatest { scrollToLatest() }
        }
        .onChange(of: detail?.exchanges.last?.assistant) { _, _ in
            if followingLatest { scrollToLatest() }
        }
    }
}

struct IOSComposer: View {
    @ObservedObject var model: AgenthailIOSModel
    @ObservedObject var draft: ComposerDraft
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let session: SessionState
    let detail: SessionDetail
    @State private var showingModels = false
    @State private var showingTurnSettings = false
    @State private var showingPrompts = false
    @State private var loadedModels: [ModelOption]?
    @State private var completionState = CompletionState(session: "", caret: 0)
    @State private var editor = IOSComposerEditor()

    private var steering: Bool { detail.session.status == "busy" && detail.session.waitingOn == nil && detail.capabilities.steer && model.busyDelivery(for: detail.session.id) == "steer" }
    private var sending: Bool { model.sendingSessionIDs.contains(session.id) }
    private var permissionMode: String? {
        guard detail.capabilities.permissionMode else { return nil }
        return model.sessionRequests?.permissionMode
    }

    private var stashActionTitle: String {
        let empty = ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerAttachments)
        if !empty { return model.stashes[session.id] == nil ? "Stash draft" : "Swap with stashed draft" }
        return "Bring back stashed draft"
    }

    @ViewBuilder private var composerMenu: some View {
        Button { showingPrompts = true } label: { Label("Recent Prompts", systemImage: "clock.arrow.circlepath") }
        let empty = ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerAttachments)
        if !empty || model.stashes[session.id] != nil {
            Button { model.toggleStash() } label: { Label(stashActionTitle, systemImage: "tray.and.arrow.down") }
        }
    }

    private var canSend: Bool {
        !sending && !model.pendingControls.contains(session.id)
            && !ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerAttachments)
            && (steering || detail.capabilities.send)
    }

    @ViewBuilder private var turnControls: some View {
        if detail.capabilities.attach && detail.capabilities.send {
            ComposerAttachmentMenu(model: model)
                .disabled(sending)
        }
        if detail.capabilities.model {
            Button { showingModels = true } label: { modelLabel }
            .buttonStyle(.plain)
            .disabled(model.pendingControls.contains(session.id))
            .accessibilityLabel("Change model, " + modelName)
            .accessibilityIdentifier("composer-model-picker")
        } else { modelLabel }
        if let mode = permissionMode {
            Button { Task { await model.setPermissionMode(SessionRequests.cycled(from: mode), sessionID: session.id) } } label: {
                Text(SessionRequests.permissionModeLabel(mode)).font(.footnote.weight(.medium))
                    .lineLimit(1)
                    .padding(.horizontal, 10).frame(minHeight: 44)
                    .overlay(Capsule().strokeBorder(Color.secondary.opacity(0.4)).frame(height: 28))
            }
            .buttonStyle(.plain)
            .layoutPriority(1)
            .accessibilityLabel("Mode, " + SessionRequests.permissionModeLabel(mode))
            .accessibilityHint("Changes to the next mode")
            .accessibilityIdentifier("composer-mode")
        } else if session.surface == "codex" && detail.capabilities.send {
            let mode = model.turnSettings(for: session.id).mode
            Button {
                var settings = model.turnSettings(for: session.id)
                settings.mode = TurnSettings.Mode.cycled(from: settings.mode)
                model.setTurnSettings(settings, for: session.id)
            } label: {
                Text(mode == .plan ? "Plan" : mode == .default ? "Work" : "Session").font(.footnote.weight(.medium))
                    .lineLimit(1)
                    .padding(.horizontal, 10).frame(minHeight: 44)
                    .overlay(Capsule().strokeBorder(Color.secondary.opacity(0.4)).frame(height: 28))
            }
            .buttonStyle(.plain)
            .layoutPriority(1)
            .accessibilityLabel("Mode, " + (mode == .plan ? "Plan" : mode == .default ? "Work" : "session setting"))
            .accessibilityHint("Switches between Work and Plan for the next message")
            .accessibilityIdentifier("composer-mode")
        }
        if session.surface == "codex" && detail.capabilities.send {
            Button { showingTurnSettings = true } label: {
                Image(systemName: model.turnSettings(for: session.id).isEmpty ? "slider.horizontal.3" : "slider.horizontal.3.circle.fill")
                    .font(.body).frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Effort and plan mode")
            .accessibilityIdentifier("composer-turn-settings")
        }
    }

    @ViewBuilder private var sendControls: some View {
        if detail.session.status == "busy" && detail.capabilities.interrupt {
            Button { model.action("interrupt", session: session) } label: {
                Image(systemName: "stop.fill").font(.system(size: 17, weight: .semibold)).frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .background(Color.primary.opacity(0.08), in: Circle())
            .accessibilityLabel("Stop current turn")
            .disabled(model.pendingControls.contains(session.id))
        }
        Button { model.send(to: session) } label: {
            if sending { ProgressView().frame(width: 44, height: 44) }
            else { Image(systemName: "arrow.up").font(.system(size: 18, weight: .semibold)).frame(width: 44, height: 44) }
        }
        .buttonStyle(.plain)
        .foregroundStyle(canSend ? AnyShapeStyle(.white) : AnyShapeStyle(PhonePalette.muted))
        .background(canSend ? AnyShapeStyle(PhonePalette.accent) : AnyShapeStyle(Color.clear), in: Circle())
        .overlay(Circle().strokeBorder(canSend ? AnyShapeStyle(Color.clear) : AnyShapeStyle(PhonePalette.muted), lineWidth: 1))
        .accessibilityLabel(steering ? "Send steering instruction" : "Send message")
        .disabled(!canSend)
        .contextMenu { composerMenu }
        .accessibilityAction(named: "Recent prompts") { showingPrompts = true }
        .accessibilityAction(named: stashActionTitle) { model.toggleStash() }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 6) {
                if let status = model.deliveryStatus[session.id] {
                    ComposerReceipt(model: model, sessionID: session.id, status: status)
                }
                if !model.composerAttachments.isEmpty {
                    ComposerAttachmentStrip(model: model)
                }
                if let problem = model.attachmentProblem {
                    Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed).padding(.horizontal, 8)
                        .accessibilityIdentifier("attachment-problem")
                }
                if !session.sharedHolders.isEmpty {
                    IOSHolderPicker(model: model, session: session)
                }
                if model.stashes[session.id] != nil {
                    let empty = ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerAttachments)
                    Button { model.toggleStash() } label: {
                        Label(empty ? "Bring back stashed draft" : "Draft stashed · returns after you send", systemImage: "tray.and.arrow.down")
                            .font(.footnote).foregroundStyle(PhonePalette.muted)
                            .padding(.horizontal, 8).frame(minHeight: 44, alignment: .leading)
                    }
                    .buttonStyle(.plain)
                    .disabled(!empty)
                    .accessibilityIdentifier("composer-stash")
                }
                if !completion.items.isEmpty || completion.notice != nil {
                    CompletionRow(suggestions: completion.items, notice: completion.notice, choose: accept) {
                        updateCompletion { $0.dismissed = draft.text }
                    }
                }
                IOSComposerTextView(
                    text: $draft.text,
                    placeholder: steering ? "Steer this turn…" : "Message this agent…",
                    label: steering ? "Instruction for the current turn" : "Message to this agent",
                    lines: dynamicTypeSize.isAccessibilitySize ? 1 ... 2 : 1 ... 6,
                    pasteAttachments: detail.capabilities.attach && detail.capabilities.send && !sending ? { model.paste($0) } : nil,
                    editor: editor,
                    onCaret: { caret in updateCompletion { $0.caret = caret } }
                )
                .padding(.horizontal, 8).padding(.top, 8)
                let row = HStack(spacing: 8) { turnControls; Spacer(minLength: 0); sendControls }
                if dynamicTypeSize.isAccessibilitySize {
                    ViewThatFits(in: .horizontal) {
                        row
                        VStack(alignment: .leading, spacing: 8) {
                            turnControls
                            HStack(spacing: 8) { Spacer(minLength: 0); sendControls }
                        }
                    }
                } else {
                    row
                }
            }
            .padding(10)
            .floatingGlass(in: RoundedRectangle(cornerRadius: 26, style: .continuous))
            .overlay { RoundedRectangle(cornerRadius: 26).strokeBorder(Color.primary.opacity(0.06)) }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("composer")
        }
        .frame(maxWidth: SessionStyle.readingWidth)
        .frame(maxWidth: .infinity)
        .padding(.horizontal).padding(.vertical, 10)
        .sheet(isPresented: $showingPrompts) {
            RecentPromptsSheet(model: model, session: session) { text in
                draft.text = draft.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? text : draft.text + "\n\n" + text
            }
        }
        .sheet(isPresented: $showingModels) {
            SearchableModelSelectionSheet(initialOptions: loadedModels ?? detail.models ?? [], currentSelectedID: detail.model, allowsDefault: false, onSelect: { value in
                if let value {
                    var settings = model.turnSettings(for: session.id)
                    settings.effort = nil
                    model.setTurnSettings(settings, for: session.id)
                    model.action("model", session: session, model: value)
                }
            }, reload: { try await loadModels() })
        }
        .sheet(isPresented: $showingTurnSettings) {
            NavigationStack {
                Form {
                    TurnSettingsView(settings: Binding(get: { model.turnSettings(for: session.id) }, set: { model.setTurnSettings($0, for: session.id) }), modelOption: selectedModelOption, enabled: !steering && !sending)
                }
                .navigationTitle("Effort and mode")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { showingTurnSettings = false } } }
                .task { if selectedModelOption == nil { _ = try? await loadModels() } }
            }
        }
        .onChange(of: session.id) { _, _ in loadedModels = nil }
        .task(id: mentionRequest) { await loadMentionFiles(mentionRequest) }
    }

    private var scopedCompletion: CompletionState { completionState.scoped(to: session.id, draft: draft.text) }

    private func updateCompletion(_ change: (inout CompletionState) -> Void) {
        var state = scopedCompletion
        change(&state)
        completionState = state
    }

    private var completionToken: CompletionToken? {
        guard scopedCompletion.dismissed != draft.text else { return nil }
        return CompletionToken.at(draft.text, caret: min(scopedCompletion.caret, (draft.text as NSString).length))
    }

    private var mentionRequest: MentionRequest? {
        completionToken.flatMap { $0.trigger == .mention ? MentionRequest(session: session.id, query: $0.query) : nil }
    }

    private var completion: (items: [CompletionItem], notice: String?) {
        guard let token = completionToken else { return ([], nil) }
        let runtime = model.store.commands[session.id]
        let mentioning = token.trigger == .mention
        let files = scopedCompletion.files
        let items = ComposerCompletion.items(for: token, appCommands: [], runtime: runtime, sessions: mentioning ? ComposerCompletion.sessions(model.snapshot?.sessions ?? [], from: session) : [], files: mentioning && files?.query == token.query ? files?.files ?? [] : [])
        return (items, ComposerCompletion.notice(for: token, runtime: runtime))
    }

    private func loadMentionFiles(_ request: MentionRequest?) async {
        guard let request else { return }
        try? await Task.sleep(for: .milliseconds(120))
        guard !Task.isCancelled else { return }
        let found = await model.sessionFiles(request.session, query: request.query)
        guard !Task.isCancelled else { return }
        updateCompletion { $0.files = (request.query, found ?? []) }
    }

    private func accept(_ item: CompletionItem) {
        guard let token = completionToken else { return }
        let change = ComposerCompletion.edit(item, in: draft.text, at: token)
        editor.replace(change.range, with: change.replacement, caret: change.caret)
    }

    private var modelName: String { detail.model ?? SessionStyle.agentName(detail.session.surface) }
    private var selectedModelOption: ModelOption? {
        let options = loadedModels ?? detail.models ?? []
        return options.first { $0.id == detail.model } ?? (detail.model == nil ? options.first { $0.default == true } : nil)
    }
    private func loadModels() async throws -> [ModelOption] {
        let options = try await model.creationModels(surface: session.surface, host: session.remoteHostID)
        loadedModels = options
        return options
    }
    private var modelLabel: some View {
        Text(modelName).font(.caption.weight(.medium)).lineLimit(1).truncationMode(.middle)
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: Capsule())
    }

}

extension EnvironmentValues {
    @Entry var editMessage: ((String) -> Void)? = nil
}

struct IOSMessage: View {
    let label: String
    let text: String
    var timestamp: String? = nil
    var files: [AttachedFile] = []
    /// How long the turn this reply ended took, from the host.
    var finished: String? = nil
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.editMessage) private var editMessage
    @State private var showingFullMessage = false
    private var isUser: Bool { label == "You" }
    private var isLongMessage: Bool { isUser && (text.count > 700 || text.components(separatedBy: .newlines).count > 10) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if isUser {
                if !text.isEmpty {
                    Text(isLongMessage ? String(text.prefix(700)) : text)
                        .font(.body)
                        .lineSpacing(4)
                        .lineLimit(isLongMessage ? 6 : nil)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .textSelection(.enabled)
                }
                ForEach(Array(files.enumerated()), id: \.offset) { _, file in
                    AttachedFileChip(file: file)
                }
                if isLongMessage {
                    Button("Read full message") { showingFullMessage = true }
                        .font(.subheadline.weight(.medium))
                        .frame(minHeight: 44)
                }
            } else {
                SessionMarkdown(text: text, readingStyle: true)
            }
            footer
        }
        .padding(isUser ? 14 : 0)
        .background(isUser ? SessionStyle.surface : .clear, in: RoundedRectangle(cornerRadius: 16))
        .padding(.leading, isUser && !dynamicTypeSize.isAccessibilitySize ? 16 : 0)
        .padding(.vertical, 4)
        .contextMenu {
            Button("Copy", systemImage: "doc.on.doc") { UIPasteboard.general.string = text }
            if isUser, let editMessage {
                Button("Edit and resend", systemImage: "pencil") { editMessage(text) }
            }
            ShareLink(item: text) { Label("Share text", systemImage: "square.and.arrow.up") }
        }
        .sheet(isPresented: $showingFullMessage) {
            TranscriptDocument(title: "Your message", text: text, markdown: false)
        }
    }

    @ViewBuilder private var footer: some View {
        if isUser {
            HStack {
                Spacer(minLength: 0)
                SessionTimestamp(value: timestamp)
            }
        } else {
            HStack(spacing: 6) {
                Text("✦").font(.caption).foregroundStyle(PhonePalette.accentText).accessibilityHidden(true)
                if let finished {
                    Text(TurnClock.footer(duration: finished, at: timestamp)).font(.caption).foregroundStyle(PhonePalette.muted)
                } else {
                    SessionTimestamp(value: timestamp)
                }
                Spacer(minLength: 0)
            }
        }
    }
}

// The file lives on the host that owns the session, so the phone offers its
// stored path rather than the file.
struct AttachedFileChip: View {
    let file: AttachedFile
    @State private var copied = false

    var body: some View {
        Button {
            UIPasteboard.general.string = file.path
            copied = true
        } label: {
            HStack(spacing: 10) {
                Image(systemName: "doc").foregroundStyle(PhonePalette.muted)
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.name).font(.subheadline.weight(.medium)).lineLimit(1)
                    Text(copied ? "Path copied" : file.sizeLabel ?? "File")
                        .font(.caption).foregroundStyle(PhonePalette.muted)
                }
                Spacer(minLength: 0)
                Image(systemName: copied ? "checkmark" : "doc.on.doc").font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            .padding(.horizontal, 12)
            .frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: RoundedRectangle(cornerRadius: 12))
        }
        .buttonStyle(.plain)
        .disabled(file.path.isEmpty)
        .accessibilityLabel("\(file.name), \(file.sizeLabel ?? "file")")
        .accessibilityHint("Copies the path where the agent can read this file")
        .accessibilityIdentifier("attached-file")
        .task(id: copied) {
            guard copied else { return }
            try? await Task.sleep(for: .seconds(2))
            copied = false
        }
    }
}

struct IOSTimelineRow: View {
    let item: TimelineItem
    var compactContext = true
    var finished: String? = nil
	var bodyLoader: ((TimelineItem) async -> RetainedBodyResult?)? = nil
    var attachmentLoader: ((TimelineItem) async -> Data?)? = nil
    var onInspect: () -> Void = {}
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var rows
	@State private var loadedBody: RowChoice<RetainedBodyResult>?

    var body: some View {
        if item.kind == "done" {
            EmptyView()
        } else if let agent = item.agent, agent.isIncoming {
            IOSAgentMessageRow(item: item, agent: agent)
        } else if let agent = item.agent, agent.isSend, compactContext {
            IOSAgentSendRow(call: item, agent: agent)
        } else if compactContext, let title = TranscriptContext.title(for: item) {
            ContextRecordRow(title: title, item: item)
        } else if item.kind == "notice" {
            TranscriptDivider(text: "\(item.title): \(item.noticeText)")
        } else if item.isSaid {
            IOSMessage(label: messageLabel, text: item.text, timestamp: item.timestamp, files: item.files ?? [], finished: finished)
            if item.truncated { ShortenedNote() }
        } else if item.kind == "attachment" {
            AttachmentRow(item: item, loader: attachmentLoader.map { loader in { await loader(item) } })
        } else if item.kind == "toolCall" {
            ToolActivityRow(call: item, results: [], standaloneRecord: true)
        } else if TimelineGroup.isTurnDuration(item) {
            Text(TurnClock.footer(duration: item.text, at: item.timestamp))
                .font(.caption)
                .foregroundStyle(PhonePalette.muted)
                .padding(.vertical, 4)
        } else if item.kind == "event", TranscriptDivider.fits(item.text) {
            TranscriptDivider(text: [eventLabel, TurnClock.time(item.timestamp)].compactMap { $0 }.joined(separator: " · "))
            if item.truncated { ShortenedNote() }
        } else {
            let open = rows.current(opened) ?? rows.isOpen(item.id) ?? (item.kind == "toolResult" && ToolRunSummary.isFailure(item))
            VStack(alignment: .leading, spacing: 8) {
                Button {
                    onInspect()
                    opened = rows.choice(!open)
                    rows.set(item.id, open: !open)
                } label: {
                    ToolLineLabel(verb: lineVerb, object: firstLine, isCode: item.kind == "toolResult", failed: item.kind == "toolResult" && ToolRunSummary.isFailure(item), trailing: open ? "chevron.down" : "chevron.right")
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("record-" + item.id)
                .accessibilityValue(open ? "Expanded" : "Collapsed")
                if open {
                    Group {
                        if item.kind == "toolResult" { ToolOutput(result: item) }
                        else {
                            SessionMarkdown(text: item.text, readingStyle: item.kind == "reasoning")
                            if item.truncated { ShortenedNote() }
                        }
                    }
                    .padding(.leading, ToolLineLabel.indent)
                } else if item.truncated { ShortenedNote().padding(.leading, ToolLineLabel.indent) }
				let retainedBody = rows.current(loadedBody) ?? rows.body(item.id)
				if item.bodyRef != nil, retainedBody == nil, let bodyLoader {
					Button("Load retained body") {
						Task {
							guard let body = await bodyLoader(item) else { return }
							loadedBody = rows.choice(body)
							rows.set(item.id, body: body)
						}
					}.font(.caption)
				} else if let retainedBody {
                    SessionMarkdown(text: retainedBody.text, readingStyle: item.kind == "reasoning")
                    if let error = retainedBody.error {
                        Label(error, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(PhonePalette.muted)
                    }
                }
            }
            .contextMenu { CopyCallID(item: item) }
        }
    }

    private var lineVerb: String {
        switch item.kind {
        case "reasoning": "Thought"
        case "toolResult": "Output"
        default: item.title
        }
    }
    private var firstLine: String {
        item.text.split(separator: "\n", omittingEmptySubsequences: true).first.map(String.init) ?? ""
    }

    private var messageLabel: String {
        if item.role == "user" { return "You" }
        if item.isInstructions { return "Instructions" }
        if item.title.contains("commentary") { return "Update" }
        if item.title.contains("analysis") { return "Reasoning" }
        return "Assistant"
    }
    private var eventLabel: String { item.eventLine }
}

/// Call ids are for debugging, so they are copied from a menu rather than shown.
struct CopyCallID: View {
    let item: TimelineItem

    var body: some View {
        if let id = item.callId, !id.isEmpty {
            Button("Copy call ID", systemImage: "number") { UIPasteboard.general.string = id }
        }
    }
}

struct AttachmentRow: View {
    let item: TimelineItem
    let loader: (() async -> Data?)?
    @State private var data: Data?
    @State private var attempted = false
    @State private var retryCount = 0
    @Environment(\.transcriptRows) private var rows

    private var failedKey: String { "attachment-failed-" + item.id }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let data, let image = Self.downsampledImage(data) {
                Image(uiImage: image).resizable().scaledToFit().frame(maxHeight: 320).clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                Label(item.text.isEmpty ? "Image attachment" : item.text, systemImage: "photo").foregroundStyle(PhonePalette.muted)
                if attempted || rows.isOpen(failedKey) == true, loader != nil { Button("Try again") { retryCount += 1 }.font(.caption) }
            }
            SessionTimestamp(value: item.timestamp)
        }
        .padding(.vertical, 4)
        .contextMenu { CopyCallID(item: item) }
        .task(id: "\(item.id)-\(retryCount)") {
            guard let loader, data == nil, retryCount > 0 || rows.isOpen(failedKey) != true else { return }
            attempted = true
            data = await loader()
            if !Task.isCancelled { rows.set(failedKey, open: data == nil) }
        }
    }

    private static func downsampledImage(_ data: Data) -> UIImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: 2048
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return UIImage(cgImage: image)
    }
}

struct SessionSummary: View {
    let detail: SessionDetail
    let openDetails: () -> Void

    var body: some View {
        Button(action: openDetails) {
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 8) {
                    status
                    Text(detail.modelLabel ?? SessionStyle.agentName(detail.session.surface)).lineLimit(1)
                    Spacer(minLength: 4)
                    context
                    Image(systemName: "chevron.down").font(.caption2)
                }
                VStack(alignment: .leading, spacing: 4) {
                    HStack { status; Spacer(); context }
                    Text(detail.modelLabel ?? SessionStyle.agentName(detail.session.surface))
                }
            }
            .font(.footnote)
            .foregroundStyle(PhonePalette.muted)
            .frame(maxWidth: SessionStyle.readingWidth)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 16)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityHint("Open session details, context, model and controls")
    }
    private var backgroundWaiting: Bool { detail.session.status != "busy" && detail.session.backgroundTasks?.running == true }
    private var status: some View {
        TimelineView(.everyMinute) { context in
            statusLabel(now: context.date)
        }
    }
    private func statusLabel(now: Date) -> some View {
        Label(detail.waitingPlace?.label ?? (detail.session.status == "busy" ? detail.session.workingLabel(now: now) : detail.limitLabel ?? (backgroundWaiting ? "Waiting on background work" : detail.session.status.capitalized)),
              systemImage: detail.session.waitingOn != nil ? "questionmark.bubble" : detail.session.status == "busy" ? "waveform" : detail.limitLabel != nil ? "gauge.with.dots.needle.100percent" : backgroundWaiting ? "hourglass" : "circle")
        .font(.subheadline)
        .foregroundStyle(detail.session.status == "busy" ? AnyShapeStyle(PhonePalette.accentText) : AnyShapeStyle(PhonePalette.muted))
    }
	@ViewBuilder private var context: some View {
		if let context = detail.context {
			Group {
				if let fraction = context.fraction {
					Text("\(context.windowEstimated == true ? "~" : "")\(Int(fraction * 100))% context").monospacedDigit()
				} else {
					Text("\(context.usedTokens.formatted()) tokens").monospacedDigit()
				}
			}
			.foregroundStyle(context.level == .full ? PhonePalette.failed : context.level == .high ? PhonePalette.attention : PhonePalette.muted)
			.accessibilityLabel(context.spokenLabel)
        }
    }
}

struct SessionInspector: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    let initialDetail: SessionDetail
    @Environment(\.dismiss) private var dismiss
    @State private var showingModels = false
    @State private var renamingClaude = false
    @State private var movingTo: LimitResumeState.MoveOffer?
    @State private var claudeName = ""
    @State private var adopting = false
    private var detail: SessionDetail {
        guard let liveDetail = model.selectedDetail, liveDetail.session.id == session.id else { return initialDetail }
        return liveDetail
    }
    private var liveSession: SessionState { model.snapshot?.sessions.first { $0.id == session.id } ?? session }
    var body: some View {
        NavigationStack {
            List {
                Section {
                    LabeledContent("Agent", value: detail.session.surface.capitalized)
                    LabeledContent("Status", value: detail.waitingPlace?.label ?? detail.limitLabel ?? (liveSession.isWaitingOnBackground ? "Waiting on background work" : detail.session.status.capitalized))
                    if let value = detail.modelLabel { LabeledContent("Model", value: value) }
                    if let configRoot = liveSession.claudeConfigRoot { LabeledContent("Config folder", value: configRoot.summary) }
                    if liveSession.canAdopt { Button("Open in Agenthail", systemImage: "arrow.down.app") { adopting = true } }
                    if let value = detail.session.cwd, !value.isEmpty { LabeledContent("Workspace", value: value).textSelection(.enabled) }
                    if let value = detail.session.source { LabeledContent("Source", value: value) }
                    if let value = detail.session.transport { LabeledContent("Connection", value: value) }
                    if let value = detail.readSource, !value.isEmpty { LabeledContent("Activity source", value: value) }
                    if let value = detail.readError, !value.isEmpty { LabeledContent("Activity warning", value: value).foregroundStyle(PhonePalette.muted) }
                    LabeledContent("Session ID", value: detail.session.id).textSelection(.enabled)
                } header: {
                    Text("Session").accessibilityIdentifier("session-inspector-anchor")
                }
                if let background = liveSession.backgroundTasks {
                    Section("Background work") {
                        Text(background.summary)
                        ForEach(background.tasks ?? []) { task in
                            Text(task.line).foregroundStyle(task.ambient == true ? AnyShapeStyle(PhonePalette.muted) : AnyShapeStyle(.primary))
                        }
                        if let note = background.sourceNote { Text(note).font(.footnote).foregroundStyle(PhonePalette.muted) }
                    }
                }
                Section("Context") {
                    if let context = detail.context {
                        LabeledContent("Used tokens", value: context.usedTokens.formatted())
						LabeledContent(context.windowEstimated == true ? "Estimated window" : (context.contextWindowSource == "configured" ? "Configured window" : "Context window"), value: context.contextWindow > 0 ? context.contextWindow.formatted() : "Unavailable")
                        if let value = context.inputTokens { LabeledContent("Input", value: value.formatted()) }
                        if let value = context.cachedInputTokens { LabeledContent("Cached input", value: value.formatted()) }
                        if let value = context.outputTokens { LabeledContent("Output", value: value.formatted()) }
                        if let value = context.reasoningOutputTokens { LabeledContent("Reasoning output", value: value.formatted()) }
                        if let value = context.cumulativeTokens { LabeledContent("Cumulative tokens", value: value.formatted()) }
                        LabeledContent("Compactions", value: context.compactionCount.formatted())
                        if let value = context.reclaimedTokens { LabeledContent("Tokens reclaimed", value: value.formatted()) }
                        if context.compacting { Label("Compacting context", systemImage: "arrow.down.right.and.arrow.up.left") }
                    } else { Text("This agent has not reported context usage.").foregroundStyle(PhonePalette.muted) }
                }
                SessionEditingControls(model: model, detail: detail)
                Section {
                    NavigationLink {
                        QueueListView(model: model, sessionID: session.id) { id in
                            dismiss()
                            model.openSession(id)
                        }
                    } label: { Label("Session inbox", systemImage: "tray") }
                }
                if let goal = detail.goal {
                    Section("Goal") {
                        if !goal.objective.isEmpty { Text(goal.objective).textSelection(.enabled) }
                        LabeledContent("Status", value: goal.displayStatus)
                            .foregroundStyle(goal.needsAttention ? AnyShapeStyle(PhonePalette.attention) : AnyShapeStyle(.primary))
                        if let value = goal.timeUsedSeconds { LabeledContent("Elapsed", value: formatGoalDuration(value)) }
                        if let value = goal.tokensUsed { LabeledContent("Tokens used", value: value.formatted()) }
                        if let value = goal.tokenBudget { LabeledContent("Token budget", value: value.formatted()) }
                        if let value = goal.createdAt { LabeledContent("Created", value: value) }
                        if let value = goal.updatedAt { LabeledContent("Updated", value: value) }
                        if let reason = goal.reason { LabeledContent("Reason") { Text(reason).textSelection(.enabled) } }
                    }
                }
                if let runs = detail.claudeRuns, !runs.isEmpty {
                    Section("Claude runs") {
                        ForEach(runs) { run in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(run.jobId).font(.body.weight(.medium)).textSelection(.enabled)
                                if let state = run.providerState, !state.isEmpty { LabeledContent("Provider state", value: state) }
                                if let type = run.runType, !type.isEmpty { LabeledContent("Run type", value: type) }
                                if let updated = run.updatedAt, !updated.isEmpty { LabeledContent("Updated", value: updated) }
                                if !run.recordPath.isEmpty { Text(run.recordPath).font(.footnote.monospaced()).foregroundStyle(PhonePalette.muted).textSelection(.enabled) }
                            }
                        }
                    }
                }
                let children = model.subagents(of: session.id)
                let subagentsLoading = model.fetchedSubagents.loading(liveSession)
                let subagentsError = model.fetchedSubagents.errors[session.id]
                if !children.isEmpty || subagentsLoading || subagentsError != nil {
                    Section("Subagents") {
                        if subagentsLoading || subagentsError != nil {
                            Text(subagentsError ?? "Loading subagents").foregroundStyle(PhonePalette.muted)
                        }
                        ForEach(children) { child in
                            Button {
                                dismiss()
                                model.openSession(child.id)
                            } label: {
                                HStack {
                                    Text(SessionFamilies.label(child)).lineLimit(1)
                                    Spacer(minLength: 0)
                                    if child.isWorking { Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(PhonePalette.accentText) }
                                }
                            }
                            .accessibilityIdentifier("subagent-" + child.id)
                        }
                    }
                }
                if let links = detail.claudeSubagents, !links.isEmpty {
                    let list = ClaudeSubagentList(links, total: detail.claudeSubagentsTotal, expanded: model.showsAllClaudeSubagents)
                    Section {
                        ForEach(list.shown) { link in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack(alignment: .firstTextBaseline) {
                                    Text(link.title).font(.body.weight(.medium)).lineLimit(2)
                                    Spacer(minLength: 0)
                                    if link.working == true {
                                        Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(PhonePalette.accentText)
                                    } else if let value = link.lastActive, let date = TimestampParser.date(value) {
                                        Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.caption).foregroundStyle(PhonePalette.muted)
                                    }
                                }
                                if let kind = link.kindLabel { Text(kind).font(.caption).foregroundStyle(PhonePalette.muted) }
                            }
                            .accessibilityElement(children: .combine)
                        }
                        if list.earlier > 0 {
                            Button(list.earlierLabel) { model.showAllClaudeSubagents() }
                        }
                    } header: {
                        Text("Claude subagents")
                    } footer: {
                        Text(list.summary)
                    }
                }
                if let errors = detail.metadataErrors, !errors.isEmpty {
                    Section("Metadata warnings") {
                        ForEach(errors.keys.sorted(), id: \.self) { key in
                            if let message = errors[key] {
                                LabeledContent(key, value: message)
                            }
                        }
                    }
                }
                Section("Controls") {
                    IOSSessionProjectControls(model: model, session: session)
                    if session.runtime?.focusable == true {
                        Button("Open in host", systemImage: "arrow.up.forward.app") { model.focusSession(session); dismiss() }
                    }
                    if detail.readOnly { Label(detail.readOnlyReason, systemImage: "lock").font(.footnote) }
                    else {
                        if detail.capabilities.model {
                            Button("Change model") { showingModels = true }
                                .disabled(model.pendingControls.contains(session.id))
                        }
                        if detail.capabilities.permissionMode {
                            let current = model.sessionRequests?.permissionMode ?? ""
                            Menu {
                                ForEach(SessionRequests.permissionModes, id: \.self) { mode in
                                    Button(SessionRequests.permissionModeLabel(mode)) { Task { await model.setPermissionMode(mode, sessionID: session.id) } }
                                        .disabled(mode == current)
                                }
                            } label: {
                                LabeledContent("Permission mode", value: SessionRequests.permissionModeLabel(current))
                            }
                        }
                        if detail.capabilities.commands {
                            Group {
                                Button("Check runtime status") { model.action("status", session: session) }
                                Button("Inspect context") { model.action("context", session: session) }
                                Button("Check usage") { model.action("usage", session: session) }
                                Menu("Change effort") {
                                    ForEach(["low", "medium", "high", "max", "auto"], id: \.self) { effort in
                                        Button(effort.capitalized) { model.action("effort", session: session, message: effort) }
                                    }
                                }
                                Button("Rename Claude session") { claudeName = session.name; renamingClaude = true }
                            }.disabled(model.pendingControls.contains(session.id))
                        }
                        if let stop = detail.limitResume, !detail.readOnly {
                            Button("Resume now", systemImage: "play.fill") { model.action("limit-resume", session: session); dismiss() }
                            if stop.isWaiting { Button("Don't resume", systemImage: "pause.circle") { model.action("limit-resume-cancel", session: session) } }
                            if let offer = stop.moveTo { Button("Move to \(offer.name)", systemImage: "person.crop.circle.badge.checkmark") { movingTo = offer } }
                        }
                        if detail.capabilities.compact { Button("Compact context", systemImage: "arrow.down.right.and.arrow.up.left") { model.action("compact", session: session); dismiss() } }
                        if detail.capabilities.interrupt && detail.session.status == "busy" { Button("Stop current turn", systemImage: "stop.fill", role: .destructive) { model.action("interrupt", session: session); dismiss() } }
                        LabeledContent("Send messages", value: detail.capabilities.send ? "Available" : "Unavailable")
                        LabeledContent("Steer active turns", value: detail.capabilities.steer ? "Available" : "Unavailable")
                    }
                }
                if let output = model.controlOutput[session.id], !output.isEmpty {
                    Section("Control result") { Text(output).font(.footnote).textSelection(.enabled) }
                }
            }
            .navigationTitle("Session details").navigationBarTitleDisplayMode(.inline)
            .alert("Rename Claude session", isPresented: $renamingClaude) {
                TextField("Session name", text: $claudeName)
                Button("Cancel", role: .cancel) {}
                Button("Rename") { model.action("rename", session: session, message: claudeName) }
                    .disabled(claudeName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            } message: { Text("Changes the Claude session name, not its Agenthail alias.") }
            .confirmationDialog(movingTo.map { "Move this session to \($0.name)?" } ?? "", isPresented: Binding(get: { movingTo != nil }, set: { if !$0 { movingTo = nil } }), titleVisibility: .visible, presenting: movingTo) { offer in
                Button("Move to \(offer.name)") { model.moveToAccount(session, root: offer.root); dismiss() }
                Button("Cancel", role: .cancel) {}
            } message: { offer in
                Text(["It stops here and continues under that account.", offer.reason.map { "Chosen for \($0)." }].compactMap { $0 }.joined(separator: " "))
            }
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .sheet(isPresented: $adopting) { IOSAdoptSessionSheet(model: model, session: model.routedSession(liveSession) ?? liveSession, sessionID: liveSession.id) }
            .task(id: SubagentFetchKey(liveSession, revision: model.fetchedSubagents.revision(session.id))) { await model.loadSubagents(of: liveSession) }
            .sheet(isPresented: $showingModels) {
                SearchableModelSelectionSheet(initialOptions: detail.models ?? [], currentSelectedID: detail.model, allowsDefault: false, onSelect: { value in
                    if let value {
                        var settings = model.turnSettings(for: session.id)
                        settings.effort = nil
                        model.setTurnSettings(settings, for: session.id)
                        model.action("model", session: session, model: value)
                        dismiss()
                    }
                }, reload: { try await model.creationModels(surface: session.surface, host: session.remoteHostID) })
            }
        }
    }
}

func formatGoalDuration(_ seconds: Int) -> String {
    if seconds < 60 { return "\(seconds)s" }
    let minutes = seconds / 60
    let remainingSeconds = seconds % 60
    if minutes < 60 { return "\(minutes)m \(remainingSeconds)s" }
    return "\(minutes / 60)h \(minutes % 60)m"
}

struct IOSSessionRow: View {
    let session: SessionState
    var title: String? = nil
    var family: SessionFamily? = nil
    var showsHost = true
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    private var displayTitle: String {
        title ?? SessionStyle.title(session)
    }

    @ViewBuilder
    private func meta(spaced: Bool) -> some View {
        Text(SessionStyle.agentName(session.surface)).font(.caption.weight(.semibold)).foregroundStyle(PhonePalette.muted)
            .lineLimit(1).fixedSize()
        if let host = session.shortRemoteHostName, showsHost || session.isUnavailable {
            IOSHostBadge(name: host, unavailable: session.isUnavailable)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("On \(host)")
        }
        if session.isWaitingOnYou {
            HStack(spacing: 4) {
                Image(systemName: "questionmark.bubble")
                Text(session.waitingPlace?.label ?? "Waiting on you")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.attention)
            .lineLimit(1)
            .fixedSize()
        } else if session.isUnavailable {
            Text("Unavailable")
                .font(.caption)
                .foregroundStyle(PhonePalette.failed)
                .lineLimit(1)
                .fixedSize()
        } else if let limit = session.limitLabel, !session.isWorking {
            HStack(spacing: 4) {
                Image(systemName: "gauge.with.dots.needle.100percent")
                Text(limit)
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.muted)
            .lineLimit(1)
            .fixedSize()
        } else if session.isWorking {
            HStack(spacing: 4) {
                Image(systemName: "waveform")
                Text("Working")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.working)
            .lineLimit(1)
            .fixedSize()
        } else if session.isWaitingOnBackground {
            HStack(spacing: 4) {
                Image(systemName: "hourglass")
                Text("Background work")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.working)
            .lineLimit(1)
            .fixedSize()
            .accessibilityLabel("Waiting on background work")
        }
        if let plan = session.livePlan {
            HStack(spacing: 4) {
                Image(systemName: "checklist")
                Text(plan.badge)
            }
            .font(.caption.monospacedDigit())
            .foregroundStyle(PhonePalette.muted)
            .lineLimit(1)
            .fixedSize()
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(plan.detail)
        }
        if spaced { Spacer(minLength: 0) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(displayTitle).font(.body.weight(.semibold)).lineLimit(dynamicTypeSize.isAccessibilitySize ? nil : 2).foregroundStyle(.primary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let value = session.lastActive, let date = TimestampParser.date(value) {
                    Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.caption).foregroundStyle(PhonePalette.muted)
                        .lineLimit(1).fixedSize()
                }
            }
            let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(alignment: .firstTextBaseline, spacing: 8))
            if dynamicTypeSize.isAccessibilitySize {
                VStack(alignment: .leading, spacing: 4) { meta(spaced: false) }
            } else {
                ViewThatFits(in: .horizontal) {
                    HStack(alignment: .firstTextBaseline, spacing: 8) { meta(spaced: true) }
                    VStack(alignment: .leading, spacing: 4) { meta(spaced: false) }
                }
            }
            if session.isUnavailable, let reason = session.unavailableReason {
                Text(reason).font(.caption).foregroundStyle(PhonePalette.muted)
            } else if session.isWorking, !session.isWaitingOnYou, let activity = session.activity {
                TimelineView(.everyMinute) { context in
                    if let note = activity.quietNote(now: context.date) {
                        Text(note).font(.caption).foregroundStyle(PhonePalette.muted)
                    }
                }
            }
            if session.queueCount > 0 || session.isReadOnly || (family?.workingSubagents ?? 0) > 0 || !session.sharedHolders.isEmpty {
                layout {
                    if let family, family.workingSubagents > 0 {
                        Label(SessionFamilies.subagentSummary(family), systemImage: "person.2")
                            .font(.caption)
                            .foregroundStyle(PhonePalette.working)
                    }
                    if session.queueCount > 0 { Label("\(session.queueCount) waiting", systemImage: "tray").font(.caption) }
                    if !session.sharedHolders.isEmpty { Label("\(session.sharedHolders.count) terminals", systemImage: "terminal").font(.caption) }
                    if session.isReadOnly { Label("Read only", systemImage: "lock").font(.caption) }
                }.foregroundStyle(PhonePalette.muted)
            }
        }
        .padding(.vertical, 8)
        .accessibilityElement(children: .combine)
    }
}

/// A one-line host label for remote sessions and projects. It truncates
/// rather than wrapping so it never squeezes the row's status.
struct IOSHostBadge: View {
    let name: String
    var unavailable = false

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: unavailable ? "wifi.slash" : "server.rack").imageScale(.small)
            Text(name).lineLimit(1).truncationMode(.tail)
        }
        .font(.caption)
        .foregroundStyle(unavailable ? AnyShapeStyle(PhonePalette.failed) : AnyShapeStyle(PhonePalette.muted))
        .frame(maxWidth: 110, alignment: .leading)
        .fixedSize(horizontal: false, vertical: true)
        .layoutPriority(-1)
    }
}

struct SettingsView: View {
    @ObservedObject var model: AgenthailIOSModel
    var body: some View {
        List {
            Group {
                Section {
                    NavigationLink("Add a computer") { PairingForm(model: model, adding: true) }
                }
                ForEach(model.hosts) { host in
                    Section {
                        Label(connectionLabel(host), systemImage: connectionIcon(host))
                        if let error = host.error { Text(error).font(.footnote).foregroundStyle(PhonePalette.muted) }
                        if let build = host.build {
                            LabeledContent("Agenthail", value: build.label).font(.footnote).textSelection(.enabled)
                        }
                        ForEach(host.surfaces) { surface in
                            VStack(alignment: .leading, spacing: 4) {
                                LabeledContent(surface.name.capitalized, value: surface.connected ? "Needs attention" : "Unavailable")
                                DisclosureGroup("Details") {
                                    if surface.runtime?.advice.isEmpty ?? true, let detail = surface.healthDetail {
                                        Text(detail).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                    }
                                    ForEach(surface.runtime?.advice ?? [], id: \.self) { line in
                                        Text(line).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                    }
                                }
                                .font(.footnote)
                            }
                        }
                        ForEach(host.signin) { consumer in
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Browser sign-in: \(consumer.label)").font(.headline)
                                Text(consumer.resolvedLabel).font(.footnote)
                                if let problem = consumer.problem {
                                    Text(problem).font(.footnote).foregroundStyle(PhonePalette.attention).textSelection(.enabled)
                                }
                                if let unavailable = consumer.unavailable {
                                    Text(unavailable).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                }
                            }
                        }
                        ForEach(host.peers) { peer in
                            VStack(alignment: .leading, spacing: 4) {
                                LabeledContent(peer.name, value: peer.stateLabel)
                                if let build = peer.build {
                                    Text(build.label).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                }
                                if let detail = peer.detail, !detail.isEmpty {
                                    Text(detail).font(.footnote).foregroundStyle(PhonePalette.muted)
                                }
                                if let notice = peer.notice, !notice.isEmpty {
                                    Text(notice).font(.footnote).foregroundStyle(PhonePalette.attention).lineLimit(3)
                                }
                            }
                        }
                        if let person = host.person {
                            VStack(alignment: .leading, spacing: 4) {
                                LabeledContent("Your name", value: person.name)
                                Text(person.set ? "Set in Settings on \(host.name) or with agenthail person name." : "\(host.name)'s account name. Set yours in Settings on \(host.name) or with agenthail person name.")
                                    .font(.footnote).foregroundStyle(PhonePalette.muted)
                            }
                        }
                        if model.hosts.count > 1 { LabeledContent("Notifications", value: host.notifications) }
                        Button("Reconnect") { model.reconnect(host: host.id) }
                        Button("Remove \(host.name)", role: .destructive) { model.removeHost(host.id) }
                    } header: {
                        Text(host.name)
                    } footer: {
                        if !host.signin.isEmpty {
                            Text("Change browser sign-in in Settings > Browser Sign-in on \(host.name), or run agenthail signin use there.")
                        } else {
                            Text(host.address)
                        }
                    }
                }
                if model.hosts.count > 1 {
                    Section {
                        Picker("Orchestrator", selection: Binding(get: { model.voiceHost?.id ?? "" }, set: { model.voiceHostID = $0 })) {
                            ForEach(model.hosts) { Text($0.name).tag($0.id) }
                        }
                    } header: {
                        Text("Voice")
                    } footer: {
                        Text("Talk to orchestrator calls this computer.")
                    }
                }
                Section("Notifications") {
                    LabeledContent("Status", value: model.notificationStatus)
                    if model.notificationStatus == "Enabled" {
                        Button("Turn off notifications", role: .destructive) { model.turnOffNotifications() }
                    } else if model.notificationStatus == "Not allowed" {
                        Button("Open iOS Settings") { model.openNotificationSettings() }
                    } else if model.notificationStatus == "Setup failed" {
                        Button("Retry notification setup") { model.requestNotifications() }
                    } else {
                        Button("Enable notifications") { model.requestNotifications() }
                    }
                    Text("Your computers send an alert when an agent finishes or fails.").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
            }
            .listRowBackground(GlassRowBackground(selected: false))
        }
        .phoneBackdrop(.pane)
        .navigationTitle("Settings")
    }

    private func connectionLabel(_ host: PairedHostStatus) -> String {
        if host.error != nil { return "Connection interrupted" }
        if host.reconnecting { return "Reconnecting live updates" }
        return host.reachable ? "Connected over Tailscale" : "Not connected"
    }

    private func connectionIcon(_ host: PairedHostStatus) -> String {
        if host.error != nil { return "wifi.exclamationmark" }
        if host.reconnecting { return "arrow.triangle.2.circlepath" }
        return host.reachable ? "lock.shield.fill" : "wifi.slash"
    }
}

struct IOSAdoptSessionSheet: View {
    @ObservedObject var model: AgenthailIOSModel
    @StateObject private var controller: SessionAdoptController
    @Environment(\.dismiss) private var dismiss

    private let sessionID: String

    init(model: AgenthailIOSModel, session: SessionState, sessionID: String) {
        self.model = model
        self.sessionID = sessionID
        _controller = StateObject(wrappedValue: SessionAdoptController(session: session))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text("Agenthail stops the process that runs the conversation once it is idle and continues it in its own host, in the same config folder.")
                        .font(.footnote)
                        .foregroundStyle(PhonePalette.muted)
                }
                switch controller.phase {
                case .ready, .adopting:
                    EmptyView()
                case .confirmTerminal(let message):
                    Section {
                        Text(message + " Stopping it ends the terminal session; the conversation continues in Agenthail.")
                        Button("Stop Terminal and Open", role: .destructive) { run(stopTerminal: true) }
                        Button("Cancel") { controller.cancelConfirmation() }
                    }
                case .done(let summary):
                    Section { Text(summary) }
                case .failed(let message):
                    Section { Text(message).foregroundStyle(PhonePalette.failed) }
                }
            }
            .navigationTitle("Open in Agenthail")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    if case .done = controller.phase {} else { Button("Cancel") { dismiss() } }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if case .done = controller.phase {
                        Button("Done") { dismiss() }
                    } else {
                        Button(controller.phase == .adopting ? "Opening…" : "Open") { run(stopTerminal: false) }
                            .disabled(!controller.canAdopt)
                    }
                }
            }
        }
    }

    private func run(stopTerminal: Bool) {
        guard let api = model.routedAPI(for: sessionID) else { return }
        Task {
            await controller.adopt(api: api, stopTerminal: stopTerminal)
            if case .done = controller.phase { _ = await model.refresh(fresh: true) }
        }
    }
}

struct RecentPromptsSheet: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    let use: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var allSessions = false
    @State private var prompts: [PromptItem] = []
    @State private var problems: [String] = []
    @State private var loading = true

    var body: some View {
        NavigationStack {
            List {
                Picker("Show", selection: $allSessions) {
                    Text("This session").tag(false)
                    Text("All sessions").tag(true)
                }
                .pickerStyle(.segmented)
                ForEach(problems, id: \.self) { Text($0).font(.footnote).foregroundStyle(PhonePalette.failed) }
                if prompts.isEmpty && !loading {
                    Text(query.isEmpty ? "Nothing sent yet." : "No sent prompt matches.").foregroundStyle(PhonePalette.muted)
                }
                ForEach(prompts) { prompt in
                    Button {
                        use(prompt.text)
                        dismiss()
                    } label: {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(prompt.text).lineLimit(3).foregroundStyle(.primary)
                            if allSessions, let title = model.snapshot?.sessions.first(where: { $0.id == prompt.sessionId })?.title {
                                Text(title).font(.footnote).foregroundStyle(PhonePalette.muted)
                            }
                        }
                    }
                    .swipeActions(edge: .trailing) {
                        if !allSessions, model.canFork(session), prompt.turnId != nil {
                            Button("Fork from here") { fork(prompt) }.tint(PhonePalette.accentText)
                        }
                    }
                    .contextMenu {
                        Button { use(prompt.text); dismiss() } label: { Label("Use This Prompt", systemImage: "text.insert") }
                        if !allSessions, model.canFork(session), prompt.turnId != nil {
                            Button { fork(prompt) } label: { Label("Fork from Here", systemImage: "arrow.triangle.branch") }
                        }
                    }
                }
            }
            .searchable(text: $query, prompt: "Search what you sent")
            .navigationTitle("Recent Prompts")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } } }
            .task(id: "\(allSessions)\u{0}\(query)") {
                loading = true
                try? await Task.sleep(for: .milliseconds(query.isEmpty ? 0 : 250))
                guard !Task.isCancelled else { return }
                let found = await model.recentPrompts(sessionID: allSessions ? nil : session.id, query: query.trimmingCharacters(in: .whitespacesAndNewlines))
                guard !Task.isCancelled else { return }
                var seen = Set<String>()
                prompts = found.prompts.filter { seen.insert($0.text).inserted }
                problems = found.problems
                loading = false
            }
        }
    }

    private func fork(_ prompt: PromptItem) {
        Task {
            do {
                try await model.fork(session, before: prompt)
                dismiss()
            } catch {
                problems = [AgenthailAPIError.userMessage(error)]
            }
        }
    }
}
