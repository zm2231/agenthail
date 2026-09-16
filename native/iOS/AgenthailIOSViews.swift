import SwiftUI

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
            model.openNotification(sessionID)
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
        guard let detail = model.selectedDetail, detail.session.id == sessionID else { return nil }
        return SessionState(id: sessionID, surface: detail.session.surface, name: detail.session.name, alias: detail.alias,
                            status: detail.session.status, lastActive: detail.session.lastActive, queueCount: 0, open: false,
                            current: false, currentReason: nil, capabilities: detail.capabilities, readOnly: detail.readOnly,
                            readOnlyReason: detail.readOnlyReason)
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
    @State private var scope = SessionScope.recent
    @State private var showingNewSession = false
    @State private var search = ""
    @State private var collapsedWorkspaces: Set<String> = []

    private enum SessionScope: String, CaseIterable {
        case running = "Running", recent = "Recent", all = "All"
    }

    private var sessions: [SessionState] {
        let source = model.snapshot?.sessions ?? []
        return source.filter { session in
            let matchesScope = scope == .all || (scope == .running ? session.isWorking : session.current)
            let matchesSearch = search.isEmpty || session.displayName.localizedCaseInsensitiveContains(search) || session.surface.localizedCaseInsensitiveContains(search) || (session.cwd?.localizedCaseInsensitiveContains(search) ?? false)
            return matchesScope && matchesSearch
        }
    }

    private var workspaces: [String] {
        var seen = Set<String>()
        return sessions.compactMap { session in
            let path = session.cwd ?? ""
            return seen.insert(path).inserted ? path : nil
        }
    }

    var body: some View {
        List {
            if let error = model.connectionError {
                Label(error, systemImage: "wifi.exclamationmark").font(.footnote).foregroundStyle(.secondary)
            }
            Label(model.reconnecting ? "Reconnecting to your Mac" : "Work on your Mac", systemImage: "desktopcomputer")
                .font(.subheadline).foregroundStyle(.secondary).listRowSeparator(.hidden)
            Picker("Sessions to show", selection: $scope) {
                ForEach(SessionScope.allCases, id: \.self) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .listRowSeparator(.hidden)
            if model.isPaired {
                VoiceOperatorEntry(model: model)
                    .listRowSeparator(.hidden)
            }
            ForEach(workspaces, id: \.self) { workspace in
                Section {
                    if !collapsedWorkspaces.contains(workspace) {
                        ForEach(sessions.filter { ($0.cwd ?? "") == workspace }) { session in sessionButton(session) }
                    }
                } header: {
                    Button {
                        if !collapsedWorkspaces.insert(workspace).inserted { collapsedWorkspaces.remove(workspace) }
                    } label: {
                        HStack(spacing: 8) {
                            Image(systemName: "folder")
                            VStack(alignment: .leading, spacing: 2) {
                                Text(workspace.isEmpty ? "Other locations" : URL(fileURLWithPath: workspace).lastPathComponent)
                                    .font(.subheadline.weight(.semibold))
                                if !workspace.isEmpty {
                                    Text(workspace).font(.caption2).foregroundStyle(.secondary).lineLimit(2)
                                }
                            }
                            Spacer(minLength: 0)
                            Image(systemName: collapsedWorkspaces.contains(workspace) ? "chevron.right" : "chevron.down")
                                .font(.caption.weight(.semibold))
                        }
                        .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.primary)
                    .textCase(nil)
                    .accessibilityLabel("Workspace \(workspace.isEmpty ? "Other locations" : workspace)")
                    .accessibilityValue(collapsedWorkspaces.contains(workspace) ? "Collapsed" : "Expanded")
                    .accessibilityIdentifier("workspace-" + workspace)
                }
            }
            if sessions.isEmpty && !model.searching {
                ContentUnavailableView {
                    Label(scope == .running ? "No agents working" : "No sessions here", systemImage: "bubble.left.and.bubble.right")
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
                    ForEach(model.searchResults.filter { result in !sessions.contains(where: { $0.id == result.id }) }) { result in
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

    private func sessionButton(_ session: SessionState, snippet: String? = nil) -> some View {
        Button { openSession(session.id) } label: {
            HStack(spacing: 12) {
                VStack(alignment: .leading, spacing: 5) {
                    IOSSessionRow(session: session)
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
        .accessibilityIdentifier("session-" + session.id)
    }
}

struct SessionScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    @State private var showingInfo = false
    @State private var activityOnly = false
    @State private var followingLatest = true
    @State private var atLatest = true
    @State private var userScrolling = false

    private var displayTitle: String { SessionStyle.title(session) }

    private var detail: SessionDetail? {
        model.selectedDetail?.session.id == session.id ? model.selectedDetail : nil
    }
    private var items: [TimelineItem] {
        let latest = detail?.timeline?.items ?? []
        let ids = Set(latest.map(\.id))
        return model.olderActivity.filter { !ids.contains($0.id) } + latest
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
                        if let timeline = detail.timeline, timeline.unavailableReason == nil,
                           !items.isEmpty || (model.activityCursor ?? 0) > 0 || detail.exchanges.isEmpty {
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
                                ForEach(items) { IOSTimelineRow(item: $0, compactContext: false).id($0.id) }
                            } else {
                                ForEach(TimelineGroup.make(items)) { group in
                                    CompactActivityGroup(group: group) { followingLatest = false }.id(group.id)
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
                                if !exchange.user.isEmpty { IOSMessage(label: "You", text: exchange.user, color: orange, timestamp: exchange.timestamp) }
                                if !exchange.assistant.isEmpty { IOSMessage(label: "Assistant", text: exchange.assistant, color: .secondary, timestamp: exchange.timestamp) }
                            }
                            if detail.exchanges.isEmpty {
                                ContentUnavailableView("No messages yet", systemImage: "bubble.left", description: Text("Messages will appear here as the agent works."))
                            }
                        }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .frame(maxWidth: SessionStyle.readingWidth, alignment: .leading)
                .frame(maxWidth: .infinity)
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(followingLatest ? .bottom : nil, for: .sizeChanges)
            .defaultScrollAnchor(.top, for: .alignment)
            .scrollDismissesKeyboard(.interactively)
            .onScrollPhaseChange { _, phase in
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
                atLatest = value
            }
            .refreshable { await model.refreshSession(session.id) }
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
                            Text(URL(fileURLWithPath: cwd).lastPathComponent + " · Mac").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
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
                    } label: { Image(systemName: "ellipsis") }
                    .accessibilityLabel("Session menu")
                    .disabled(detail == nil)
                }
            }
            .sheet(isPresented: $showingInfo) {
                if let detail { SessionInspector(model: model, session: session, detail: detail) }
            }
            .task(id: session.id) {
                followingLatest = true
                atLatest = true
                await model.loadSession(session.id)
                proxy.scrollTo("bottom", anchor: .bottom)
            }
            .task(id: "activity-" + session.id) {
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

    private var steering: Bool { detail.session.status == "busy" && detail.capabilities.steer }
    private var sending: Bool { model.sendingSessionIDs.contains(session.id) }

    private var canSend: Bool {
        !sending && !model.pendingControls.contains(session.id)
            && !model.composer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
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
                TextField(steering ? "Steer this turn…" : "Message this agent…", text: $model.composer, axis: .vertical)
                    .font(.body).lineLimit(1...6).textFieldStyle(.plain)
                    .padding(.horizontal, 8).padding(.top, 8)
                    .accessibilityLabel(steering ? "Instruction for the current turn" : "Message to this agent")
                    .accessibilityIdentifier("composer-input")
                let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 8)) : AnyLayout(HStackLayout(spacing: 8))
                layout {
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
        let options = try await model.creationModels(surface: session.surface)
        loadedModels = options
        return options
    }
    private var modelLabel: some View {
        Text(modelName).font(.caption.weight(.medium)).lineLimit(2)
            .padding(.horizontal, 12).frame(minHeight: 44)
            .background(Color.primary.opacity(0.05), in: Capsule())
    }

}

struct IOSMessage: View {
    let label: String
    let text: String
    let color: Color
    var timestamp: String? = nil
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var showingFullMessage = false
    private var isUser: Bool { label == "You" }
    private var isLongMessage: Bool { isUser && (text.count > 700 || text.components(separatedBy: .newlines).count > 10) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if isUser {
                Text(isLongMessage ? String(text.prefix(700)) : text)
                    .font(.body)
                    .lineSpacing(4)
                    .lineLimit(isLongMessage ? 6 : nil)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .textSelection(.enabled)
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
            ShareLink(item: text) { Label("Share text", systemImage: "square.and.arrow.up") }
        }
        .sheet(isPresented: $showingFullMessage) {
            TranscriptDocument(title: "Your message", text: text, markdown: false)
        }
    }
}

struct IOSTimelineRow: View {
    let item: TimelineItem
    var compactContext = true
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var expanded = false

    var body: some View {
        if compactContext, let title = TranscriptContext.title(for: item) {
            ContextRecordRow(title: title, item: item)
        } else if item.kind == "message" {
            IOSMessage(label: messageLabel, text: item.text, color: .secondary, timestamp: item.timestamp)
            if item.truncated { shortened }
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
            }
            .padding(.vertical, 4)
        }
    }

    private var messageLabel: String {
        if item.role == "user" { return "You" }
        if item.role == "system" || item.role == "developer" { return "Context" }
        if item.title.contains("commentary") { return "Update" }
        if item.title.contains("analysis") { return "Reasoning" }
        return "Assistant"
    }
    private var eventLabel: String {
        let title = item.title.replacingOccurrences(of: "_", with: " ").capitalized
        return item.text.isEmpty ? title : "\(title) · \(item.text)"
    }
    private var shortened: some View {
        Label("Content shortened by the host", systemImage: "text.badge.ellipsis").font(.caption).foregroundStyle(.secondary)
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
        Label(detail.session.status == "busy" ? "Working" : detail.session.status.capitalized,
              systemImage: detail.session.status == "busy" ? "waveform" : "circle")
        .font(.subheadline)
        .foregroundStyle(detail.session.status == "busy" ? SessionStyle.accent : .secondary)
    }
    @ViewBuilder private var context: some View {
        if let context = detail.context, context.contextWindow > 0 {
            Text("\(context.windowEstimated == true ? "~" : "")\(Int(context.fraction * 100))% context").monospacedDigit()
        }
    }
}

struct SessionInspector: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    let detail: SessionDetail
    @Environment(\.dismiss) private var dismiss
    @State private var showingModels = false
    var body: some View {
        NavigationStack {
            List {
                Section("Session") {
                    LabeledContent("Agent", value: detail.session.surface.capitalized)
                    LabeledContent("Status", value: detail.session.status.capitalized)
                    if let value = detail.model { LabeledContent("Model", value: value) }
                    if let value = detail.session.cwd, !value.isEmpty { LabeledContent("Workspace", value: value).textSelection(.enabled) }
                    if let value = detail.session.source { LabeledContent("Source", value: value) }
                    if let value = detail.session.transport { LabeledContent("Connection", value: value) }
                    LabeledContent("Session ID", value: detail.session.id).textSelection(.enabled)
                }
                Section("Context") {
                    if let context = detail.context {
                        LabeledContent("Used tokens", value: context.usedTokens.formatted())
                        LabeledContent(context.windowEstimated == true ? "Estimated window" : "Context window", value: context.contextWindow > 0 ? context.contextWindow.formatted() : "Unavailable")
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
                            model.openNotification(id)
                        }
                    } label: { Label("Session inbox", systemImage: "tray") }
                }
                if let goal = detail.goal, !goal.objective.isEmpty {
                    Section("Goal") { Text(goal.objective).textSelection(.enabled); LabeledContent("Status", value: goal.status) }
                }
                Section("Controls") {
                    if detail.readOnly { Label(detail.readOnlyReason, systemImage: "lock").font(.footnote) }
                    else {
                        if detail.capabilities.model {
                            Button("Change model") { showingModels = true }
                                .disabled(model.pendingControls.contains(session.id))
                        }
                        if detail.capabilities.compact { Button("Compact context", systemImage: "arrow.down.right.and.arrow.up.left") { model.action("compact", session: session); dismiss() } }
                        if detail.capabilities.interrupt && detail.session.status == "busy" { Button("Stop current turn", systemImage: "stop.fill", role: .destructive) { model.action("interrupt", session: session); dismiss() } }
                        LabeledContent("Send messages", value: detail.capabilities.send ? "Available" : "Unavailable")
                        LabeledContent("Steer active turns", value: detail.capabilities.steer ? "Available" : "Unavailable")
                    }
                }
            }
            .navigationTitle("Session details").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .sheet(isPresented: $showingModels) {
                SearchableModelSelectionSheet(initialOptions: detail.models ?? [], currentSelectedID: detail.model, allowsDefault: false, onSelect: { value in
                    if let value {
                        var settings = model.turnSettings(for: session.id)
                        settings.effort = nil
                        model.setTurnSettings(settings, for: session.id)
                        model.action("model", session: session, model: value)
                        dismiss()
                    }
                }, reload: { try await model.creationModels(surface: session.surface) })
            }
        }
    }
}

extension ISO8601DateFormatter {
    static func sessionDate(_ value: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: value) ?? ISO8601DateFormatter().date(from: value)
    }
}

struct IOSSessionRow: View {
    let session: SessionState
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    private var title: String {
        SessionStyle.title(session)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.body.weight(.medium)).lineLimit(dynamicTypeSize.isAccessibilitySize ? nil : 2).foregroundStyle(.primary)
            let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(alignment: .firstTextBaseline, spacing: 8))
            layout {
                Text(SessionStyle.agentName(session.surface)).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                if session.isWorking {
                    Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(SessionStyle.accent)
                }
                if !dynamicTypeSize.isAccessibilitySize { Spacer(minLength: 0) }
                if let value = session.lastActive, let date = ISO8601DateFormatter.sessionDate(value) {
                    Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.caption).foregroundStyle(.secondary)
                }
            }
            if session.queueCount > 0 || session.isReadOnly {
                layout {
                    if session.queueCount > 0 { Label("\(session.queueCount) waiting", systemImage: "tray").font(.caption) }
                    if session.isReadOnly { Label("Read only", systemImage: "lock").font(.caption) }
                }.foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 8)
        .accessibilityElement(children: .combine)
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
