import SwiftUI

private let orange = Color(red: 1, green: 0.37, blue: 0.16)

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
            NavigationStack { WorkView(model: model) }
                .tabItem { Label("Today", systemImage: "bolt") }
                .tag(0)
            ConversationFlow(model: model)
                .tabItem { Label("Conversations", systemImage: "bubble.left.and.bubble.right") }
                .tag(1)
            NavigationStack { SettingsView(model: model) }
                .tabItem { Label("Settings", systemImage: "gearshape") }
                .tag(2)
        }
        .tint(orange)
        .task { await model.refresh(fresh: true) }
        .onReceive(NotificationCenter.default.publisher(for: .agenthailNotificationOpened)) { notification in
            guard let sessionID = notification.object as? String else { return }
            model.openNotification(sessionID)
            selection = 1
        }
    }
}

struct ConversationFlow: View {
    @ObservedObject var model: AgenthailIOSModel
    @Environment(\.horizontalSizeClass) private var sizeClass
    @State private var path: [String] = []
    @State private var wideSelection: String?

    init(model: AgenthailIOSModel, initialSessionID: String? = nil) {
        self.model = model
        _wideSelection = State(initialValue: initialSessionID)
        _path = State(initialValue: initialSessionID.map { [$0] } ?? [])
    }

    var body: some View {
        Group {
            if sizeClass == .regular {
                NavigationSplitView {
                    ConversationListView(model: model, selectedID: $wideSelection)
                } detail: {
                    if let wideSelection { SessionRouteView(model: model, sessionID: wideSelection) }
                    else { ContentUnavailableView("Choose a conversation", systemImage: "bubble.left.and.bubble.right", description: Text("Keep your conversations alongside the session as you work.")) }
                }
            } else {
                NavigationStack(path: $path) {
                    ConversationListView(model: model)
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

struct WorkView: View {
    @ObservedObject var model: AgenthailIOSModel

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 18) {
                if let error = model.connectionError {
                    Label(error, systemImage: "wifi.exclamationmark").foregroundStyle(orange).padding()
                }
                Text("Current conversations").font(.title2.bold())
                if model.currentSessions.isEmpty {
                    ContentUnavailableView("No current conversations", systemImage: "checkmark.circle", description: Text("Current Claude Code and recent Codex work will appear here."))
                } else {
                    ForEach(model.currentSessions) { session in
                        NavigationLink {
                            SessionScreen(model: model, session: session)
                        } label: {
                            IOSSessionRow(session: session)
                        }
                        .buttonStyle(.plain)
                    }
                }
                Text("Needs attention").font(.title2.bold()).padding(.top, 12)
                NavigationLink { QueueListView(model: model) } label: { Label("Queued instructions", systemImage: "tray") }.frame(minHeight: 44)
                if (model.snapshot?.attention ?? []).isEmpty {
                    Text("You are all caught up.").foregroundStyle(.secondary).padding(.vertical, 8)
                } else {
                    ForEach(model.snapshot?.attention ?? []) { item in
                        NavigationLink {
                            SessionRouteView(model: model, sessionID: item.sessionId)
                        } label: {
                        VStack(alignment: .leading, spacing: 5) {
                            Text(item.target).fontWeight(.semibold)
                            Text(item.reason).foregroundStyle(.secondary)
                            Text(item.requestedAction).font(.caption.weight(.bold)).foregroundStyle(orange)
                        }
                        .padding().frame(maxWidth: .infinity, alignment: .leading)
                        }.buttonStyle(.plain)
                    }
                }
            }
            .padding()
        }
        .navigationTitle("Agenthail")
        .refreshable { await model.refresh(fresh: true) }
    }
}

struct ConversationListView: View {
    @ObservedObject var model: AgenthailIOSModel
    @Binding var selectedID: String?
    @State private var showHistory = false
    @State private var showingNewSession = false
    @State private var search = ""

    init(model: AgenthailIOSModel, selectedID: Binding<String?> = .constant(nil)) {
        self.model = model
        _selectedID = selectedID
    }

    private var sessions: [SessionState] {
        let source = showHistory ? (model.snapshot?.sessions ?? []) : model.currentSessions
        return search.isEmpty ? source : source.filter { $0.displayName.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        List(selection: $selectedID) {
            if let error = model.connectionError { Label(error, systemImage: "wifi.exclamationmark").font(.footnote).foregroundStyle(.secondary) }
            Picker("Scope", selection: $showHistory) {
                Text("Current").tag(false)
                Text("Saved").tag(true)
            }
            .pickerStyle(.segmented)
            ForEach(sessions) { session in
                NavigationLink(value: session.id) { IOSSessionRow(session: session) }
            }
            if sessions.isEmpty && !model.searching {
                ContentUnavailableView(search.isEmpty ? "No conversations in this view" : "No saved matches", systemImage: "bubble.left", description: Text(search.isEmpty ? "Start a conversation with the + button, or try Saved." : "Search with at least three characters to include older Codex sessions."))
            }
            if search.count >= 3 {
                Section("Older Codex conversations") {
                    if model.searching { ProgressView("Searching history") }
                    if let error = model.searchError { Text(error).font(.footnote).foregroundStyle(.secondary) }
                    ForEach(model.searchResults.filter { result in !sessions.contains(where: { $0.id == result.id }) }) { result in
                        NavigationLink(value: result.id) {
                            VStack(alignment: .leading, spacing: 4) {
                                IOSSessionRow(session: result.session)
                                if let snippet = result.snippet { Text(snippet).font(.caption).foregroundStyle(.secondary).lineLimit(2) }
                            }
                        }
                    }
                }
            }
        }
        .task(id: search) { await model.searchSessions(search) }
        .listStyle(.plain)
        .searchable(text: $search, prompt: "Find a conversation")
        .navigationTitle("Conversations")
        .toolbar { ToolbarItem(placement: .primaryAction) { Button("New conversation", systemImage: "plus") { model.creationError = nil; showingNewSession = true } } }
        .sheet(isPresented: $showingNewSession) { NewSessionSheet(model: model) }
        .refreshable { await model.refresh(fresh: true) }
    }
}

struct SessionScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    @State private var showingInfo = false
    @State private var activityOnly = false
    @State private var followingLatest = true

    private var detail: SessionDetail? {
        model.selectedDetail?.session.id == session.id ? model.selectedDetail : nil
    }
    private var items: [TimelineItem] {
        let latest = detail?.timeline?.items ?? []
        let ids = Set(latest.map(\.id))
        return (model.olderActivity.filter { !ids.contains($0.id) } + latest).filter { !activityOnly || $0.kind != "message" }
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 20) {
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
                        SessionSummary(detail: detail) { showingInfo = true }
                        if let timeline = detail.timeline, timeline.unavailableReason == nil, (!timeline.items.isEmpty || (timeline.nextBefore ?? 0) > 0) {
                            Picker("Transcript filter", selection: $activityOnly) {
                                Text("Conversation").tag(false)
                                Text("Activity").tag(true)
                            }.pickerStyle(.segmented)
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
                            ForEach(TimelineGroup.make(items)) { group in CompactActivityGroup(group: group) }
                            if items.isEmpty { Text("No tool activity in this part of the conversation.").foregroundStyle(.secondary) }
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
                                if !exchange.user.isEmpty { IOSMessage(label: "You", text: exchange.user, color: orange) }
                                if !exchange.assistant.isEmpty { IOSMessage(label: session.surface.capitalized, text: exchange.assistant, color: .secondary) }
                            }
                            if detail.exchanges.isEmpty {
                                ContentUnavailableView("No messages yet", systemImage: "bubble.left", description: Text("Messages will appear here as the agent works."))
                            }
                        }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .frame(maxWidth: 760, alignment: .leading)
                .frame(maxWidth: .infinity)
                .padding()
            }
            .scrollDismissesKeyboard(.interactively)
            .simultaneousGesture(DragGesture().onChanged { _ in followingLatest = false })
            .refreshable { await model.refreshSession(session.id) }
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: 0) {
                    if !followingLatest {
                        Button {
                            followingLatest = true
                            proxy.scrollTo("bottom", anchor: .bottom)
                        } label: { Label("Jump to latest", systemImage: "arrow.down").padding(10) }
                    }
                    if let detail {
                        if detail.readOnly || (!detail.capabilities.send && !detail.capabilities.steer) {
                            Label(detail.readOnlyReason.isEmpty ? "This session does not support messages." : detail.readOnlyReason, systemImage: "lock")
                                .font(.footnote).foregroundStyle(.secondary).padding().frame(maxWidth: .infinity)
                        } else {
                            IOSComposer(model: model, session: session, detail: detail)
                        }
                    }
                }.background(.bar)
            }
            .navigationTitle(session.displayName)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Session details", systemImage: "info.circle") { showingInfo = true }.disabled(detail == nil)
                }
            }
            .sheet(isPresented: $showingInfo) {
                if let detail { SessionInspector(model: model, session: session, detail: detail) }
            }
            .task(id: session.id) {
                await model.loadSession(session.id)
                proxy.scrollTo("bottom", anchor: .bottom)
            }
            .task(id: "activity-" + session.id) {
                while !Task.isCancelled {
                    do { try await Task.sleep(for: .seconds(4)) } catch { return }
                    if detail?.session.status == "busy" || detail?.timeline?.unavailableReason != nil {
                        await model.refreshSession(session.id)
                    }
                }
            }
            .onChange(of: detail?.timeline?.items.last?.id) { _, _ in
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
    let session: SessionState
    let detail: SessionDetail

    private var steering: Bool { detail.session.status == "busy" && detail.capabilities.steer }
    private var sending: Bool { model.sendingSessionIDs.contains(session.id) }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ViewThatFits(in: .horizontal) {
                HStack {
                    Text(detail.model ?? detail.session.surface.capitalized)
                    if let context = detail.context, context.contextWindow > 0 { Text("· \(Int(context.fraction * 100))% context") }
                    Spacer()
                    Text(steering ? "Steering current turn" : "New instruction")
                }
                Text(steering ? "Steering current turn" : "New instruction")
            }.font(.caption).foregroundStyle(.secondary)
            if let status = model.deliveryStatus[session.id] {
                Text(status).font(.footnote).foregroundStyle(.secondary)
            }
            HStack(alignment: .bottom, spacing: 10) {
                TextField(steering ? "Steer this turn" : "Message this agent", text: $model.composer, axis: .vertical)
                    .lineLimit(1...6).textFieldStyle(.plain).padding(12)
                    .background(Color.secondary.opacity(0.12), in: RoundedRectangle(cornerRadius: 16))
                    .accessibilityLabel(steering ? "Instruction for the current turn" : "Message to this agent")
                Button { model.send(to: session) } label: {
                    if sending { ProgressView().frame(width: 44, height: 44) }
                    else { Image(systemName: "arrow.up").font(.headline).frame(width: 44, height: 44) }
                }
                .buttonStyle(.borderedProminent).buttonBorderShape(.circle).tint(orange)
                .accessibilityLabel(steering ? "Send steering instruction" : "Send message")
                .disabled(sending || model.composer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || (!steering && !detail.capabilities.send))
            }
        }
        .frame(maxWidth: 760)
        .frame(maxWidth: .infinity)
        .padding(.horizontal).padding(.vertical, 10)
    }
}

struct IOSMessage: View {
    let label: String
    let text: String
    let color: Color
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(label).font(.subheadline.weight(.semibold)).foregroundStyle(.secondary)
            if let markdown = try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                Text(markdown).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
            } else {
                Text(text).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .contextMenu { ShareLink(item: text) { Label("Share text", systemImage: "square.and.arrow.up") } }
    }
}

struct IOSTimelineRow: View {
    let item: TimelineItem
    @State private var expanded = false
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if item.kind == "message" {
                IOSMessage(label: item.role == "user" ? "You" : item.title.capitalized, text: item.text, color: .secondary)
            } else {
                DisclosureGroup(isExpanded: $expanded) {
                    VStack(alignment: .leading, spacing: 8) {
                        if let callID = item.callId {
                            Text("Call: \(callID)").font(.caption.monospaced()).foregroundStyle(.secondary).textSelection(.enabled)
                        }
                        if item.kind == "toolCall" { ToolContentView(item: item) }
                        else {
                            Text(item.text.isEmpty ? "No additional details were recorded." : item.text)
                                .font(item.kind == "toolResult" ? .system(.callout, design: .monospaced) : .body)
                                .textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }.padding(.top, 8)
                } label: {
                    Label {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(item.title).font(.callout.weight(.semibold))
                            if let status = item.status, !status.isEmpty { Text(status.capitalized).font(.caption).foregroundStyle(.secondary) }
                            if !expanded && !item.text.isEmpty { Text(item.kind == "toolCall" ? ToolPresentation(name: item.title, text: item.text).summary : item.text).font(.caption).foregroundStyle(.secondary).lineLimit(2) }
                        }
                    } icon: { Image(systemName: symbol) }
                    .frame(minHeight: 44)
                }
                .tint(.primary)
            }
            if item.truncated { Label("This output was shortened", systemImage: "text.badge.ellipsis").font(.caption).foregroundStyle(.secondary) }
            if let timestamp = item.timestamp, let date = ISO8601DateFormatter.sessionDate(timestamp) {
                Text(date, format: .dateTime.month(.abbreviated).day().hour().minute()).font(.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 4)
    }
    private var symbol: String {
        switch item.kind {
        case "toolCall": return "terminal"
        case "toolResult": return item.status == "error" ? "exclamationmark.circle" : "text.alignleft"
        case "reasoning": return "text.bubble"
        case "attachment": return "paperclip"
        default: return "clock.arrow.circlepath"
        }
    }
}

struct SessionSummary: View {
    let detail: SessionDetail
    let openDetails: () -> Void
    var body: some View {
        Button(action: openDetails) {
            VStack(alignment: .leading, spacing: 8) {
                ViewThatFits(in: .horizontal) {
                    HStack { status; Spacer(); modelLabel }
                    VStack(alignment: .leading, spacing: 4) { status; modelLabel }
                }
                if let context = detail.context, context.contextWindow > 0 {
                    ProgressView(value: context.fraction)
                        .accessibilityLabel("Context used").accessibilityValue("\(Int(context.fraction * 100)) percent")
                    Text("\(Int(context.fraction * 100))% context · \(context.usedTokens.formatted()) tokens")
                        .font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
                }
                if let goal = detail.goal, !goal.objective.isEmpty {
                    Label(goal.objective, systemImage: "target").font(.callout).lineLimit(2)
                }
            }.frame(maxWidth: .infinity, alignment: .leading).padding(.vertical, 4)
        }.buttonStyle(.plain).accessibilityHint("Opens context, goal, model and session controls")
    }
    private var status: some View { Label(detail.session.status.capitalized, systemImage: detail.session.status == "busy" ? "waveform" : "bubble.left").font(.subheadline.weight(.semibold)) }
    private var modelLabel: some View { Text(detail.model ?? detail.session.surface.capitalized).font(.subheadline).foregroundStyle(.secondary) }
}

struct SessionInspector: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    let detail: SessionDetail
    @Environment(\.dismiss) private var dismiss
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
                Section { NavigationLink { QueueListView(model: model, sessionID: session.id) } label: { Label("Queued instructions", systemImage: "tray") } }
                if let goal = detail.goal, !goal.objective.isEmpty {
                    Section("Goal") { Text(goal.objective).textSelection(.enabled); LabeledContent("Status", value: goal.status) }
                }
                Section("Controls") {
                    if detail.readOnly { Label(detail.readOnlyReason, systemImage: "lock").font(.footnote) }
                    else {
                        if detail.capabilities.model, let options = detail.models, !options.isEmpty {
                            Menu("Change model") {
                                ForEach(options) { option in Button(option.displayName) { model.action("model", session: session, model: option.id); dismiss() } }
                            }
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
    var body: some View {
        HStack(spacing: 12) {
            Circle().fill(session.isWorking ? orange : session.current ? .green : .secondary).frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 3) {
                Text(session.displayName).fontWeight(.semibold).lineLimit(2)
                Text("\(session.surface == "claude" ? "Claude Code" : session.surface.capitalized) · \(session.isWorking ? "Working" : session.status.capitalized)").font(.subheadline).foregroundStyle(.secondary)
                if session.queueCount > 0 { Text("\(session.queueCount) queued").font(.caption).foregroundStyle(.secondary) }
                if session.isReadOnly { Label("Read only", systemImage: "lock").font(.caption).foregroundStyle(.secondary) }
            }
        }
        .padding(.vertical, 6)
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
