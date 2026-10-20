import SwiftUI
import UIKit
import ImageIO

private let orange = SessionStyle.accent

struct AgenthailIOSRoot: View {
    @ObservedObject var model: AgenthailIOSModel

    var body: some View {
        Group {
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
        .confirmationDialog("This Mac could not be reached", isPresented: $model.showForgetMacConfirmation, titleVisibility: .visible) {
            Button("Forget this Mac on iPhone", role: .destructive) { model.forgetThisMac() }
            Button("Keep connection", role: .cancel) {}
        } message: {
            Text("Forget the saved connection only if this Mac is gone or you want to pair with another Mac.")
        }
        .confirmationDialog(model.pairingConfirmationTitle, isPresented: $model.showPairingConfirmation, titleVisibility: .visible) {
            Button(model.isPaired ? "Replace Mac" : "Connect") { model.confirmPairing() }
            Button("Cancel", role: .cancel) { model.cancelPairing() }
        } message: {
            Text(model.pairingConfirmationMessage)
        }
    }
}

struct PairingScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    @State private var scanning = false
    @State private var pastedURL = ""

    var body: some View {
        NavigationStack {
            ScrollView {
            VStack(spacing: 24) {
                ZStack {
                    RoundedRectangle(cornerRadius: 28).fill(orange)
                    Text("A").font(.system(size: 58, weight: .black)).foregroundStyle(.black)
                }
                .frame(width: 112, height: 112)
                VStack(spacing: 8) {
                    Text("Connect to your agents").font(.largeTitle.bold()).multilineTextAlignment(.center)
                    Text("On your Mac, open Agenthail, choose Operations, then Pair an iPhone.")
                        .foregroundStyle(.secondary).multilineTextAlignment(.center)
                }
                .padding(.horizontal)
                Button {
                    scanning = true
                } label: {
                    Label("Scan pairing code", systemImage: "qrcode.viewfinder").frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent).tint(orange).controlSize(.large)
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
                Text("Your conversations stay between this phone and your Mac over Tailscale.")
                    .font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center).padding(.horizontal, 30)
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
        }
    }
}

struct MainTabs: View {
    @ObservedObject var model: AgenthailIOSModel
    @State private var selection = 0

    var body: some View {
        TabView(selection: $selection) {
            ConversationFlow(model: model)
                .tabItem { Label("Sessions", systemImage: "bubble.left.and.bubble.right") }
                .tag(0)
            NavigationStack { QueueListView(model: model) }
                .tabItem { Label("Inbox", systemImage: "tray") }
                .tag(1)
            NavigationStack { SettingsView(model: model) }
                .tabItem { Label("Settings", systemImage: "gearshape") }
                .tag(2)
        }
        .tint(orange)
        .task { await model.refresh(fresh: true) }
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
                    else { ContentUnavailableView("Choose a session", systemImage: "desktopcomputer", description: Text("Follow work on your Mac, review results, or give the next instruction.")) }
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

    private var tree: SessionTree {
        let sessions = SessionFamilies.build(model.snapshot?.sessions ?? []).filter { family in
            search.isEmpty || family.sessions.contains { session in
                session.displayName.localizedCaseInsensitiveContains(search) || SessionFamilies.label(session).localizedCaseInsensitiveContains(search) || session.surface.localizedCaseInsensitiveContains(search) || (session.project?.displayName.localizedCaseInsensitiveContains(search) ?? false) || (session.cwd?.localizedCaseInsensitiveContains(search) ?? false)
            }
        }.flatMap(\.sessions)
        return SessionTree.build(sessions, filter: scope, attentionSessionIDs: Set(model.snapshot?.attention.map(\.sessionId) ?? []))
    }

    private var families: [SessionFamily] {
        tree.projects.flatMap { $0.checkouts.flatMap(\.families) }
    }

    var body: some View {
        List {
            if let error = model.connectionError {
                Label(error, systemImage: "wifi.exclamationmark").font(.footnote).foregroundStyle(.secondary)
            }
            Label(model.reconnecting ? "Reconnecting to your Mac" : "Work on your Mac", systemImage: "desktopcomputer")
                .font(.subheadline).foregroundStyle(.secondary).listRowSeparator(.hidden)
            Picker("Sessions to show", selection: $scope) {
                ForEach(SessionFilter.allCases) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .listRowSeparator(.hidden)
            if model.isPaired {
                VoiceOperatorEntry(model: model)
                    .listRowSeparator(.hidden)
            }
            let tree = tree
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
                            if let host = project.host {
                                IOSHostBadge(name: host)
                            }
                            Spacer(minLength: 0)
                            Image(systemName: collapsed ? "chevron.right" : "chevron.down")
                                .font(.caption.weight(.semibold))
                        }
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
                        }
                    }
                    .accessibilityLabel(project.host.map { "Project \(project.name) on \($0)" } ?? "Project \(project.name)")
                    .accessibilityValue(collapsed ? "Collapsed" : "Expanded")
                    .accessibilityIdentifier("project-" + project.id)
                }
            }
            if families.isEmpty && !model.searching {
                ContentUnavailableView {
                    Label(scope == .running ? "No agents working" : scope == .archived ? "Nothing archived" : "No sessions here", systemImage: "bubble.left.and.bubble.right")
                } description: {
                    Text(search.isEmpty ? "Start a session, or browse All to continue earlier work." : "Try another title or search All sessions.")
                } actions: {
                    Button("New session", systemImage: "plus") { showingNewSession = true }
                }
                .listRowSeparator(.hidden)
            }
            if search.count >= 3 {
                Section("From saved history") {
                    if model.searching { ProgressView("Searching") }
                    if let error = model.searchError { Text(error).font(.footnote).foregroundStyle(.secondary) }
                    ForEach(model.searchResults.filter { result in !families.contains(where: { $0.contains(result.id) }) }) { result in
                        sessionButton(result.session, snippet: result.snippet)
                    }
                }
            }
        }
        .listStyle(.plain)
        .navigationTitle("Sessions")
        .searchable(text: $search, prompt: "Search sessions")
        .task(id: search) { await model.searchSessions(search) }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("New session", systemImage: "square.and.pencil") {
                    model.creationError = nil
                    showingNewSession = true
                }.accessibilityIdentifier("new-session")
            }
        }
        .sheet(isPresented: $showingNewSession) { NewSessionSheet(model: model) }
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
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, minHeight: 32, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.leading, 16)
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

    private func sessionButton(_ session: SessionState, title: String? = nil, family: SessionFamily? = nil, snippet: String? = nil) -> some View {
        Button { openSession(session.id) } label: {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 5) {
                    IOSSessionRow(session: session, title: title, family: family)
                    if let snippet, !snippet.isEmpty {
                        Text(snippet).font(.subheadline).foregroundStyle(.secondary).lineLimit(2)
                    }
                }
                Spacer(minLength: 0)
                if session.isWorking { WorkingIndicator() }
            }.contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .listRowBackground(selectedID == session.id ? SessionStyle.surface : Color.clear)
        .swipeActions(edge: .trailing) {
            if snippet == nil {
                Button(session.archived == true ? "Unarchive" : "Archive", systemImage: "archivebox") {
                    Task { await model.setArchived(sessionID: session.id, session.archived != true) }
                }
            }
        }
        .accessibilityIdentifier("session-" + session.id)
    }
}

struct SessionScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    @EnvironmentObject private var voiceCalls: VoiceCallCoordinator
    let session: SessionState
    @State private var showingInfo = false
    @State private var showingVoice = false
    @State private var usageMove: String?
    private var currentSession: SessionState { model.snapshot?.sessions.first { $0.id == session.id } ?? session }
    @State private var activityOnly = false
    @State private var followingLatest = true
    @State private var atLatest = true
    @State private var userScrolling = false

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

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    if let error = model.connectionError {
                        Label(error, systemImage: "wifi.exclamationmark").font(.callout).foregroundStyle(.secondary)
                    } else if model.reconnecting {
                        Label("Reconnecting live updates", systemImage: "arrow.triangle.2.circlepath").font(.callout).foregroundStyle(.secondary)
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
                            Label(warning, systemImage: "exclamationmark.bubble").font(.footnote).foregroundStyle(.secondary)
                        }
                        if let limit = detail.usageLimit {
                            VStack(alignment: .leading, spacing: 8) {
                                Label(limit.summary, systemImage: "gauge.with.dots.needle.100percent").font(.callout)
                                if let alternative = limit.alternatives?.first, currentSession.canMoveAccount {
                                    Button("Move to \(alternative.label)…") { usageMove = alternative.root }
                                        .buttonStyle(.borderedProminent)
                                } else {
                                    Text("No other signed-in Claude account on this host has usage left.").font(.footnote).foregroundStyle(.secondary)
                                }
                            }
                            .padding(12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 12))
                            .accessibilityIdentifier("usage-limit-banner")
                        }
                        if let timeline = detail.timeline,
                           Self.showsActivity(timeline: timeline, itemCount: items.count, activityCursor: model.activityCursor, exchangeCount: detail.exchanges.count) {
                            if (model.activityCursor ?? 0) > 0 {
                                Button { followingLatest = false; Task { await model.loadOlderActivity() } } label: {
                                    if model.loadingOlderActivity { ProgressView("Loading older activity") }
                                    else { Label("Load older activity", systemImage: "arrow.up") }
                                }.disabled(model.loadingOlderActivity).frame(minHeight: 44)
                            }
                            if let error = model.olderActivityError { Text(error).font(.footnote).foregroundStyle(.secondary) }
                            if timeline.truncated {
                                Label("Showing recent activity. Older activity or long output has been shortened.", systemImage: "text.badge.ellipsis")
                                    .font(.footnote).foregroundStyle(.secondary)
                            }
                            if activityOnly {
                                ForEach(items) { IOSTimelineRow(item: $0, compactContext: false, bodyLoader: { item in await model.retainedSessionBody(for: item) }, attachmentLoader: { item in await model.sessionAttachment(for: item, sessionID: session.id) }).id($0.id) }
                            } else {
                                ForEach(TimelineGroup.make(items)) { group in
                                    CompactActivityGroup(group: group, attachmentLoader: { item in await model.sessionAttachment(for: item, sessionID: session.id) }) { followingLatest = false }.id(group.id)
                                }
                            }
                            if items.isEmpty { ContentUnavailableView("Ready for your instruction", systemImage: "bubble.left", description: Text("Messages and agent activity will appear here.")) }
                        } else {
                            if let reason = detail.timeline?.unavailableReason {
                                Label(reason, systemImage: "info.circle").font(.footnote).foregroundStyle(.secondary)
                            } else {
                                Text("Message history. Detailed activity is not available for this session.").font(.footnote).foregroundStyle(.secondary)
                            }
                            if detail.transcriptTruncated == true {
                                Text("Older or oversized messages were shortened.").font(.footnote).foregroundStyle(.secondary)
                            }
                            ForEach(Array(detail.exchanges.enumerated()), id: \.offset) { _, exchange in
                                if !exchange.user.isEmpty { IOSMessage(label: exchange.promptLabel, text: exchange.user, color: orange, timestamp: exchange.timestamp) }
                                if !exchange.assistant.isEmpty { IOSMessage(label: "Assistant", text: exchange.assistant, color: .secondary, timestamp: exchange.timestamp) }
                            }
                            if detail.exchanges.isEmpty {
                                ContentUnavailableView("No messages yet", systemImage: "bubble.left", description: Text("Messages will appear here as the agent works."))
                            }
                        }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .environment(\.editMessage, detail.map { $0.capabilities.send && !$0.readOnly } == true ? { model.editAndResend($0) } : nil)
                .frame(maxWidth: SessionStyle.readingWidth, alignment: .leading)
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
            .accessibilityIdentifier("session-timeline")
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(followingLatest ? .bottom : nil, for: .sizeChanges)
            .defaultScrollAnchor(.top, for: .alignment)
            .scrollDismissesKeyboard(.interactively)
            .onScrollPhaseChange { _, phase in
#if DEBUG
                guard !ProcessInfo.processInfo.arguments.contains("--preview-session") else { return }
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
                guard !ProcessInfo.processInfo.arguments.contains("--preview-session") else { return }
#endif
                atLatest = value
            }
            .refreshable { await model.refreshSession(session.id) }
            .task(id: "\(session.id)|\(session.waitingOn?.id ?? "")|\(session.capabilities.respond)") { await model.syncRequests(for: session) }
            .safeAreaInset(edge: .top, spacing: 0) {
                if let detail { SessionSummary(detail: detail) { showingInfo = true } }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: 0) {
                    if !atLatest && detail != nil {
                        Button {
                            followingLatest = true
                            proxy.scrollTo("bottom", anchor: .bottom)
                        } label: {
                            Image(systemName: "arrow.down")
                                .font(.body.weight(.semibold))
                                .frame(width: 44, height: 44)
                                .background(.regularMaterial, in: Circle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Jump to latest")
                    }
                    if let detail, !detail.readOnly, let request = model.sessionRequests?.requests.first {
                        RequestCard(request: request, answering: model.answeringRequest != nil, accent: SessionStyle.accent) { decision in
                            Task { await model.respond(to: request, sessionID: session.id, decision: decision) }
                        }
                        .id(request.id)
                        .padding(.horizontal)
                        .padding(.top, 6)
                    } else if let detail, let waiting = detail.session.waitingOn, detail.readOnly || !detail.capabilities.respond {
                        ElsewhereWaitCard(waiting: waiting, surface: session.surface, hostName: session.runtime?.hostName, accent: SessionStyle.accent,
                                          open: session.runtime?.focusable == true ? { model.focusSession(session) } : nil)
                            .id(waiting.id)
                            .padding(.horizontal)
                            .padding(.top, 6)
                    }
                    if let detail {
                        if detail.readOnly || (!detail.capabilities.send && !detail.capabilities.steer) {
                            Label(detail.readOnlyReason.isEmpty ? "This session does not support messages." : detail.readOnlyReason, systemImage: "lock")
                                .font(.footnote).foregroundStyle(.secondary).padding().frame(maxWidth: .infinity)
                        } else {
                            IOSComposer(model: model, session: session, detail: detail)
                        }
                    }
                }
                .background(.background)
            }
            .toolbar(.hidden, for: .tabBar)
            .navigationTitle(displayTitle)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    VStack(spacing: 2) {
                        Text(displayTitle).font(.body.weight(.semibold)).lineLimit(1)
                        if let cwd = detail?.session.cwd, !cwd.isEmpty {
                            Text(URL(fileURLWithPath: cwd).lastPathComponent + " · " + (session.remoteHostID == nil ? "Mac" : session.host?.name ?? "Mac")).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
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
                        if session.canCallByVoice {
                            Button("Call this session", systemImage: "phone.fill") {
                                voiceCalls.open(targetID: session.id)
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
            .sheet(isPresented: Binding(get: { usageMove != nil }, set: { if !$0 { usageMove = nil } })) {
                IOSMoveSessionSheet(model: model, session: currentSession, adopt: false, preferredAccount: usageMove)
            }
            .sheet(isPresented: $showingVoice) {
                if let call = voiceCalls.model {
                    AgenthailVoiceOperatorSheet(model: call) { _ in }
                }
            }
            .task(id: session.id) {
                let startsAtOldest = ProcessInfo.processInfo.arguments.contains("--preview-reading-top")
                followingLatest = !startsAtOldest
                atLatest = !startsAtOldest
                await model.loadSession(session.id)
                if startsAtOldest, let first = items.first {
                    proxy.scrollTo(first.id, anchor: .top)
                } else {
                    proxy.scrollTo("bottom", anchor: .bottom)
                }
            }
            .task(id: "activity-" + session.id) {
#if DEBUG
                guard !ProcessInfo.processInfo.arguments.contains("--preview-session") else { return }
#endif
                while !Task.isCancelled {
                    do { try await Task.sleep(for: .seconds(4)) } catch { return }
                    await model.refreshSession(session.id)
                }
            }
            .onChange(of: detail?.timeline?.items) { _, _ in
                if followingLatest { proxy.scrollTo("bottom", anchor: .bottom) }
            }
            .onChange(of: detail?.exchanges.last?.assistant) { _, _ in
                if followingLatest { proxy.scrollTo("bottom", anchor: .bottom) }
            }
        }
    }
}

struct IOSComposer: View {
    @ObservedObject var model: AgenthailIOSModel
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    let session: SessionState
    let detail: SessionDetail
    @State private var showingModels = false
    @State private var showingTurnSettings = false
    @State private var loadedModels: [ModelOption]?

    private var steering: Bool { detail.session.status == "busy" && detail.session.waitingOn == nil && detail.capabilities.steer && model.snapshot?.busyDelivery == "steer" }
    private var sending: Bool { model.sendingSessionIDs.contains(session.id) }

    private var canSend: Bool {
        !sending && !model.pendingControls.contains(session.id)
            && !ComposerAttachments.isEmpty(text: model.composer, attachments: model.composerAttachments)
            && (steering || detail.capabilities.send)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let status = model.deliveryStatus[session.id] {
                NavigationLink {
                    QueueListView(model: model, sessionID: session.id)
                } label: {
                    Text("Latest instruction: " + status).font(.footnote).foregroundStyle(.secondary)
                        .padding(.horizontal, 8).frame(minHeight: 44, alignment: .leading)
                }.accessibilityHint("Open this session’s delivery inbox")
                    .accessibilityIdentifier("latest-instruction")
            }
            VStack(alignment: .leading, spacing: 6) {
                if !model.composerAttachments.isEmpty {
                    ComposerAttachmentStrip(model: model)
                }
                if let problem = model.attachmentProblem {
                    Text(problem).font(.footnote).foregroundStyle(.red).padding(.horizontal, 8)
                        .accessibilityIdentifier("attachment-problem")
                }
                TextField(steering ? "Steer this turn…" : "Message this agent…", text: $model.composer, axis: .vertical)
                    .font(.body).lineLimit(1...6).textFieldStyle(.plain)
                    .padding(.horizontal, 8).padding(.top, 8)
                    .accessibilityLabel(steering ? "Instruction for the current turn" : "Message to this agent")
                    .accessibilityIdentifier("composer-input")
                let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 8)) : AnyLayout(HStackLayout(spacing: 8))
                layout {
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
                    if session.surface == "codex" && detail.capabilities.send {
                        Button { showingTurnSettings = true } label: {
                            Image(systemName: model.turnSettings(for: session.id).isEmpty ? "slider.horizontal.3" : "slider.horizontal.3.circle.fill")
                                .font(.body).frame(width: 44, height: 44)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Effort and plan mode")
                        .accessibilityIdentifier("composer-turn-settings")
                    }
                    if !dynamicTypeSize.isAccessibilitySize { Spacer(minLength: 0) }
                    HStack(spacing: 8) {
                        if dynamicTypeSize.isAccessibilitySize { Spacer(minLength: 0) }
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
                        .foregroundStyle(canSend ? .white : .secondary)
                        .background(canSend ? SessionStyle.accent : Color.primary.opacity(0.08), in: Circle())
                        .accessibilityLabel(steering ? "Send steering instruction" : "Send message")
                        .disabled(!canSend)
                    }
                }
            }
            .padding(10)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 26))
            .overlay { RoundedRectangle(cornerRadius: 26).strokeBorder(Color.primary.opacity(0.06)) }
        }
        .frame(maxWidth: SessionStyle.readingWidth)
        .frame(maxWidth: .infinity)
        .padding(.horizontal).padding(.vertical, 10)
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
        Text(modelName).font(.caption.weight(.medium)).lineLimit(2)
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
    let color: Color
    var timestamp: String? = nil
    var files: [AttachedFile] = []
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
            HStack {
                Spacer(minLength: 0)
                SessionTimestamp(value: timestamp)
            }
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
                Image(systemName: "doc").foregroundStyle(.secondary)
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.name).font(.subheadline.weight(.medium)).lineLimit(1)
                    Text(copied ? "Path copied" : file.sizeLabel ?? "File")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
                Image(systemName: copied ? "checkmark" : "doc.on.doc").font(.footnote).foregroundStyle(.secondary)
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
	var bodyLoader: ((TimelineItem) async -> RetainedBodyResult?)? = nil
    var attachmentLoader: ((TimelineItem) async -> Data?)? = nil
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var expanded = false
	@State private var retainedBody: RetainedBodyResult?

    var body: some View {
        if compactContext, let title = TranscriptContext.title(for: item) {
            ContextRecordRow(title: title, item: item)
        } else if item.kind == "message" {
            IOSMessage(label: messageLabel, text: item.text, color: .secondary, timestamp: item.timestamp, files: item.files ?? [])
            if item.truncated { shortened }
        } else if item.kind == "attachment" {
            AttachmentRow(item: item, loader: attachmentLoader.map { loader in { await loader(item) } })
        } else if item.kind == "toolCall" {
            ToolActivityRow(call: item, results: [], standaloneRecord: true)
        } else if item.kind == "event" {
            let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(alignment: .firstTextBaseline, spacing: 8))
            layout {
                Label(eventLabel, systemImage: "clock").font(.footnote)
                if !dynamicTypeSize.isAccessibilitySize { Spacer(minLength: 0) }
                SessionTimestamp(value: item.timestamp)
            }
            .foregroundStyle(.secondary)
            .padding(.vertical, 4)
            .textSelection(.enabled)
            if item.truncated { shortened }
        } else {
            VStack(alignment: .leading, spacing: 8) {
                Button { expanded.toggle() } label: {
                    HStack(alignment: .top, spacing: 8) {
                        Image(systemName: item.kind == "reasoning" ? "text.bubble" : item.kind == "attachment" ? "paperclip" : "text.alignleft")
                        VStack(alignment: .leading, spacing: 4) {
                            Text(item.kind == "reasoning" ? "Reasoning" : item.title).font(.subheadline.weight(.medium))
                            if !expanded && !item.text.isEmpty {
                                Text(item.text).font(.footnote).foregroundStyle(.secondary).lineLimit(2)
                            }
                        }
                        Spacer(minLength: 0)
                        Image(systemName: expanded ? "chevron.down" : "chevron.right").font(.caption)
                    }
                    .frame(minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
                }.buttonStyle(.plain)
                if expanded {
                    if item.kind == "toolResult" { TranscriptCode(text: item.text) }
                    else { SessionMarkdown(text: item.text, readingStyle: item.kind == "reasoning") }
                    if let id = item.callId {
                        Text("Call \(id)").font(.caption.monospaced()).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                    SessionTimestamp(value: item.timestamp)
                }
                if item.truncated { shortened }
				if item.bodyRef != nil, retainedBody == nil, let bodyLoader {
					Button("Load retained body") { Task { retainedBody = await bodyLoader(item) } }.font(.caption)
				} else if let retainedBody {
                    SessionMarkdown(text: retainedBody.text, readingStyle: item.kind == "reasoning")
                    if let error = retainedBody.error {
                        Label(error, systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .padding(.vertical, 4)
        }
    }

    private var messageLabel: String {
        if item.role == "user" { return "You" }
        if item.isPeerMessage { return item.peerLabel }
        if item.role == "system" || item.role == "developer" { return "Context" }
        if item.title.contains("commentary") { return "Update" }
        if item.title.contains("analysis") { return "Reasoning" }
        return "Assistant"
    }
    private var eventLabel: String {
        let title = [item.title.replacingOccurrences(of: "_", with: " ").capitalized, item.status ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")
        return item.text.isEmpty ? title : "\(title) · \(item.text)"
    }
    private var shortened: some View {
        Label("Content shortened by the host", systemImage: "text.badge.ellipsis").font(.caption).foregroundStyle(.secondary)
    }
}

struct AttachmentRow: View {
    let item: TimelineItem
    let loader: (() async -> Data?)?
    @State private var data: Data?
    @State private var attempted = false
    @State private var retryCount = 0

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let data, let image = Self.downsampledImage(data) {
                Image(uiImage: image).resizable().scaledToFit().frame(maxHeight: 320).clipShape(RoundedRectangle(cornerRadius: 12))
            } else {
                Label(item.text.isEmpty ? "Image attachment" : item.text, systemImage: "photo").foregroundStyle(.secondary)
                if attempted, loader != nil { Button("Try again") { retryCount += 1 }.font(.caption) }
            }
            if let id = item.callId { Text("Call \(id)").font(.caption.monospaced()).foregroundStyle(.secondary).textSelection(.enabled) }
            SessionTimestamp(value: item.timestamp)
        }
        .padding(.vertical, 4)
        .task(id: "\(item.id)-\(retryCount)") {
            guard let loader, data == nil else { return }
            attempted = true
            data = await loader()
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
                    Text(detail.model ?? SessionStyle.agentName(detail.session.surface)).lineLimit(1)
                    Spacer(minLength: 4)
                    context
                    Image(systemName: "chevron.down").font(.caption2)
                }
                VStack(alignment: .leading, spacing: 4) {
                    HStack { status; Spacer(); context }
                    Text(detail.model ?? SessionStyle.agentName(detail.session.surface))
                }
            }
            .font(.footnote)
            .foregroundStyle(.secondary)
            .frame(maxWidth: SessionStyle.readingWidth)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 16)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .background(.bar)
        .accessibilityHint("Open session details, context, model and controls")
    }
    private var status: some View {
        Label(detail.session.waitingOn != nil ? "Waiting on you" : detail.session.status == "busy" ? "Working" : detail.session.status.capitalized,
              systemImage: detail.session.waitingOn != nil ? "questionmark.bubble" : detail.session.status == "busy" ? "waveform" : "circle")
        .font(.subheadline)
        .foregroundStyle(detail.session.status == "busy" ? SessionStyle.accent : .secondary)
    }
	@ViewBuilder private var context: some View {
		if let context = detail.context {
			if let fraction = context.fraction {
				Text("\(context.windowEstimated == true ? "~" : "")\(Int(fraction * 100))% context").monospacedDigit()
			} else {
				Text("\(context.usedTokens.formatted()) tokens").monospacedDigit()
			}
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
    @State private var claudeName = ""
    @State private var moving: Bool?
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
                    LabeledContent("Status", value: detail.session.waitingOn != nil ? "Waiting on you" : detail.session.status.capitalized)
                    if let value = detail.model { LabeledContent("Model", value: value) }
                    if let account = liveSession.claudeAccount { LabeledContent("Account", value: account.label) }
                    if liveSession.canMoveAccount { Button("Move to Another Account…", systemImage: "arrow.left.arrow.right") { moving = false } }
                    if liveSession.canAdopt { Button("Open in Agenthail", systemImage: "arrow.down.app") { moving = true } }
                    if let value = detail.session.cwd, !value.isEmpty { LabeledContent("Workspace", value: value).textSelection(.enabled) }
                    if let value = detail.session.source { LabeledContent("Source", value: value) }
                    if let value = detail.session.transport { LabeledContent("Connection", value: value) }
                    if let value = detail.readSource, !value.isEmpty { LabeledContent("Activity source", value: value) }
                    if let value = detail.readError, !value.isEmpty { LabeledContent("Activity warning", value: value).foregroundStyle(.secondary) }
                    LabeledContent("Session ID", value: detail.session.id).textSelection(.enabled)
                } header: {
                    Text("Session").accessibilityIdentifier("session-inspector-anchor")
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
                    } else { Text("This agent has not reported context usage.").foregroundStyle(.secondary) }
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
                            .foregroundStyle(goal.needsAttention ? .orange : .primary)
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
                                if !run.recordPath.isEmpty { Text(run.recordPath).font(.footnote.monospaced()).foregroundStyle(.secondary).textSelection(.enabled) }
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
                            Text(subagentsError ?? "Loading subagents").foregroundStyle(.secondary)
                        }
                        ForEach(children) { child in
                            Button {
                                dismiss()
                                model.openSession(child.id)
                            } label: {
                                HStack {
                                    Text(SessionFamilies.label(child)).lineLimit(1)
                                    Spacer(minLength: 0)
                                    if child.isWorking { Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(SessionStyle.accent) }
                                }
                            }
                            .accessibilityIdentifier("subagent-" + child.id)
                        }
                    }
                }
                if let links = detail.claudeSubagents, !links.isEmpty {
                    Section("Claude subagents") {
                        ForEach(links) { link in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack {
                                    Text(link.agentType.flatMap { $0.isEmpty ? nil : $0 } ?? link.agentId).font(.body.weight(.medium)).textSelection(.enabled)
                                    Spacer(minLength: 0)
                                    if link.working == true { Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(SessionStyle.accent) }
                                }
                                if let description = link.description, !description.isEmpty { Text(description).font(.subheadline).foregroundStyle(.secondary) }
                                if !link.transcriptPath.isEmpty { Text(link.transcriptPath).font(.footnote.monospaced()).foregroundStyle(.secondary).textSelection(.enabled) }
                            }
                        }
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
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .sheet(isPresented: Binding(get: { moving != nil }, set: { if !$0 { moving = nil } })) {
                if let adopt = moving { IOSMoveSessionSheet(model: model, session: liveSession, adopt: adopt) }
            }
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

extension ISO8601DateFormatter {
    static func sessionDate(_ value: String) -> Date? {
        (try? fractionalSessionDate.parse(value)) ?? (try? wholeSecondSessionDate.parse(value))
    }

    private static let fractionalSessionDate = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let wholeSecondSessionDate = Date.ISO8601FormatStyle()
}

struct IOSSessionRow: View {
    let session: SessionState
    var title: String? = nil
    var family: SessionFamily? = nil
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    private var displayTitle: String {
        title ?? SessionStyle.title(session)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(displayTitle).font(.body.weight(.medium)).lineLimit(dynamicTypeSize.isAccessibilitySize ? nil : 2).foregroundStyle(.primary)
            let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(alignment: .firstTextBaseline, spacing: 8))
            layout {
                Text(SessionStyle.agentName(session.surface)).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    .lineLimit(1).fixedSize()
                if let host = session.shortRemoteHostName {
                    IOSHostBadge(name: host, unavailable: session.isUnavailable)
                        .accessibilityLabel(session.isUnavailable ? "Unavailable on \(host)" : "On \(host)")
                }
                if session.isWaitingOnYou {
                    HStack(spacing: 4) {
                        Image(systemName: "questionmark.bubble")
                        Text("Waiting on you")
                    }
                    .font(.caption)
                    .foregroundStyle(SessionStyle.accent)
                    .lineLimit(1)
                    .fixedSize()
                } else if session.isWorking {
                    HStack(spacing: 4) {
                        Image(systemName: "waveform")
                        Text("Working")
                    }
                    .font(.caption)
                    .foregroundStyle(SessionStyle.accent)
                    .lineLimit(1)
                    .fixedSize()
                }
                if !dynamicTypeSize.isAccessibilitySize { Spacer(minLength: 0) }
                if let value = session.lastActive, let date = ISO8601DateFormatter.sessionDate(value) {
                    Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.caption).foregroundStyle(.secondary)
                        .lineLimit(1).fixedSize()
                }
            }
            if session.isUnavailable, let reason = session.unavailableReason {
                Text(reason).font(.caption).foregroundStyle(.secondary)
            }
            if session.queueCount > 0 || session.isReadOnly || (family?.subagentCount ?? 0) > 0 {
                layout {
                    if let family, family.subagentCount > 0 {
                        Label(SessionFamilies.subagentSummary(family), systemImage: "person.2")
                            .font(.caption)
                            .foregroundStyle(family.workingSubagents > 0 ? SessionStyle.accent : .secondary)
                    }
                    if session.queueCount > 0 { Label("\(session.queueCount) waiting", systemImage: "tray").font(.caption) }
                    if session.isReadOnly { Label("Read only", systemImage: "lock").font(.caption) }
                }.foregroundStyle(.secondary)
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
        .foregroundStyle(unavailable ? Color.orange : Color.secondary)
        .frame(maxWidth: 110, alignment: .leading)
        .fixedSize(horizontal: false, vertical: true)
        .layoutPriority(-1)
    }
}

struct SettingsView: View {
    @ObservedObject var model: AgenthailIOSModel
    var body: some View {
        List {
            Section("Connection") {
                Label(connectionLabel, systemImage: connectionIcon)
                if let error = model.connectionError { Text(error).font(.footnote).foregroundStyle(.secondary) }
                Button("Reconnect") { model.connect() }
            }
            if !attentionSurfaces.isEmpty {
                Section("Mac connections") {
                    ForEach(attentionSurfaces) { surface in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(surface.name.capitalized).font(.headline)
                            if surface.runtime?.advice.isEmpty ?? true, let detail = surface.healthDetail {
                                Text(detail).font(.footnote).foregroundStyle(.secondary)
                            }
                            ForEach(surface.runtime?.advice ?? [], id: \.self) { line in
                                Text(line).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled)
                            }
                        }
                    }
                }
            }
            if let consumers = model.snapshot?.signin, !consumers.isEmpty {
                Section {
                    ForEach(consumers) { consumer in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(consumer.label).font(.headline)
                            Text(consumer.resolvedLabel).font(.footnote)
                            if let problem = consumer.problem {
                                Text(problem).font(.footnote).foregroundStyle(.orange).textSelection(.enabled)
                            }
                        }
                    }
                } header: {
                    Text("Browser sign-in")
                } footer: {
                    Text("Change it on the Mac in Settings > Browser Sign-in, or run agenthail signin use.")
                }
            }
            if let hosts = model.snapshot?.daemon.hosts, !hosts.isEmpty {
                Section("Hosts") {
                    ForEach(hosts) { host in
                        VStack(alignment: .leading, spacing: 4) {
                            LabeledContent(host.name, value: host.stateLabel)
                            if let detail = host.detail, !detail.isEmpty {
                                Text(detail).font(.footnote).foregroundStyle(.secondary)
                            }
                            if let notice = host.notice, !notice.isEmpty {
                                Text(notice).font(.footnote).foregroundStyle(.orange)
                            }
                        }
                    }
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
                Text("Your Mac sends an alert when an agent finishes or fails.").font(.footnote).foregroundStyle(.secondary)
            }
            Section {
                Button("Disconnect this iPhone", role: .destructive) { model.unpair() }
            }
        }
        .navigationTitle("Settings")
    }

    private var attentionSurfaces: [SurfaceState] {
        (model.snapshot?.surfaces ?? []).filter(\.needsAttention)
    }

    private var connectionLabel: String {
        if model.connectionError != nil { return "Connection interrupted" }
        if model.reconnecting { return "Reconnecting live updates" }
        return "Connected over Tailscale"
    }

    private var connectionIcon: String {
        if model.connectionError != nil { return "wifi.exclamationmark" }
        if model.reconnecting { return "arrow.triangle.2.circlepath" }
        return "lock.shield.fill"
    }
}

struct IOSMoveSessionSheet: View {
    @ObservedObject var model: AgenthailIOSModel
    @StateObject private var controller: SessionMoveController
    @Environment(\.dismiss) private var dismiss

    init(model: AgenthailIOSModel, session: SessionState, adopt: Bool, preferredAccount: String? = nil) {
        self.model = model
        _controller = StateObject(wrappedValue: SessionMoveController(session: session, adopt: adopt, preferredAccount: preferredAccount))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Text(controller.adopt ? "Agenthail stops the terminal process once it is idle and runs the conversation in its own host." : "The conversation keeps its history, name and queued messages, and spends the chosen account's usage from its next turn.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                switch controller.phase {
                case .loading:
                    ProgressView()
                case .confirmTerminal(let message):
                    Section {
                        Text(message + " Stopping it ends the terminal session; the conversation continues in Agenthail.")
                        Button("Stop Terminal and Move", role: .destructive) { run(stopTerminal: true) }
                        Button("Cancel") { controller.cancelConfirmation() }
                    }
                case .done(let summary):
                    Section { Text(summary) }
                case .failed(let message):
                    Section { Text(message).foregroundStyle(.red) }
                    if !controller.adopt && !controller.accounts.isEmpty { picker }
                case .choosing, .moving:
                    if !controller.adopt { picker }
                    if controller.sharesAccount {
                        Section { Text("Both directories are signed into the same account, so the session still spends that account's usage.").font(.footnote) }
                    }
                }
            }
            .navigationTitle(controller.adopt ? "Open in Agenthail" : "Move to Account")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    if case .done = controller.phase {} else { Button("Cancel") { dismiss() } }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if case .done = controller.phase {
                        Button("Done") { dismiss() }
                    } else {
                        Button(controller.phase == .moving ? "Moving…" : (controller.adopt ? "Open" : "Move")) { run(stopTerminal: false) }
                            .disabled(!controller.canMove)
                    }
                }
            }
            .task { if let api = model.api { await controller.load(api: api) } }
        }
    }

    private var picker: some View {
        Section("Account") {
            Picker("Account", selection: $controller.selected) {
                ForEach(controller.accounts) { account in
                    Text(account.root == controller.currentRoot ? "\(account.menuLabel) · current" : account.menuLabel)
                        .tag(account.root)
                        .selectionDisabled(!account.signedIn || account.root == controller.currentRoot)
                }
            }
            .pickerStyle(.inline)
            .labelsHidden()
            .disabled(controller.phase == .moving)
        }
    }

    private func run(stopTerminal: Bool) {
        guard let api = model.api else { return }
        Task {
            await controller.move(api: api, stopTerminal: stopTerminal)
            if case .done = controller.phase { _ = await model.refresh(fresh: true) }
        }
    }
}
