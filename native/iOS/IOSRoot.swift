import SwiftUI

struct AgenthailIOSRoot: View {
    @Bindable var model: AgenthailIOSModel

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
        .transientNotice(model.projectNotice)
        .moveNoticeAlert(model)
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
    var model: AgenthailIOSModel

    var body: some View {
        NavigationStack { PairingForm(model: model) }
    }
}

struct PairingForm: View {
    var model: AgenthailIOSModel
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
    var model: AgenthailIOSModel
    @Environment(\.scenePhase) private var scenePhase
    @State private var selection = MainTab.sessions

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("MainTabs")
#endif
        let pinned = ExtensionEntries.pinned(model.extensionEntries, pins: ExtensionPins.shared.ids)
        TabView(selection: $selection) {
            ConversationFlow(model: model)
                .solidTabBar()
                .tabItem { Label("Sessions", systemImage: "bubble.left.and.bubble.right") }
                .tag(MainTab.sessions)
            NavigationStack { QueueListView(model: model) }
                .solidTabBar()
                .tabItem { Label("Inbox", systemImage: "tray") }
                .badge(model.inboxStops.count)
                .tag(MainTab.inbox)
            ForEach(pinned) { entry in
                NavigationStack { ExtensionPageScreen(model: model, entryID: entry.id) }
                    .solidTabBar()
                    .tabItem { Label { Text(entry.label) } icon: { ExtensionSymbol(name: entry.icon) } }
                    .badge(entry.badge.map { Text($0) })
                    .tag(MainTab.extensionEntry(entry.id))
            }
            NavigationStack { ExtensionsHub(model: model) }
                .solidTabBar()
                .tabItem { Label("Apps", systemImage: ExtensionEntry.fallbackIcon) }
                .tag(MainTab.extensions)
            NavigationStack { SettingsView(model: model) }
                .solidTabBar()
                .tabItem { Label("Settings", systemImage: "gearshape") }
                .tag(MainTab.settings)
        }
        .onChange(of: pinned.map(\.id)) { _, ids in
            if case .extensionEntry(let id) = selection, !ids.contains(id) { selection = .extensions }
        }
        .onChange(of: model.requestedSessionID) { _, sessionID in
            if sessionID != nil { selection = .sessions }
        }
        .task(id: model.decisionsKey) {
            try? await Task.sleep(for: .milliseconds(300))
            if !Task.isCancelled { model.decisionFeed.refresh() }
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { model.decisionFeed.reload() }
        }
        .onChange(of: PhoneRoute.shared.pending, initial: true) { _, pending in
            guard pending != nil else { return }
            PresentedSheets.whenNoDraftIsOpen {
                guard let opened = PhoneRoute.shared.take() else { return }
                model.openSession(opened.session, turn: opened.turn)
            }
        }
        .onChange(of: model.routeOpenings) { _, _ in PresentedSheets.closeAll() }
    }
}

enum MainTab: Hashable {
    case sessions
    case inbox
    case extensions
    case extensionEntry(String)
    case settings
}

struct ConversationFlow: View {
    var model: AgenthailIOSModel
    @Environment(\.horizontalSizeClass) private var sizeClass
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @State private var path: [String] = []
    @State private var wideSelection: String?

    static func splits(_ sizeClass: UserInterfaceSizeClass?, _ typeSize: DynamicTypeSize) -> Bool {
        sizeClass == .regular && !typeSize.isAccessibilitySize
    }

    init(model: AgenthailIOSModel, initialSessionID: String? = nil) {
        self.model = model
        _wideSelection = State(initialValue: initialSessionID)
        _path = State(initialValue: initialSessionID.map { [$0] } ?? [])
    }

    var body: some View {
        Group {
            if Self.splits(sizeClass, dynamicTypeSize) {
                NavigationSplitView {
                    ConversationListView(model: model, selectedID: wideSelection) { wideSelection = $0; path = [$0] }
                } detail: {
                    if let wideSelection, let projectID = ConversationRoute.projectID(wideSelection) {
                        NavigationStack { ProjectScreen(model: model, projectID: projectID) { self.wideSelection = $0; path = [$0] } }
                    } else if let wideSelection { SessionRouteView(model: model, sessionID: wideSelection) }
                    else { ContentUnavailableView("Choose a session", systemImage: "desktopcomputer", description: Text("Follow work on your computers, review results, or give the next instruction.")).phoneBackdrop(.pane) }
                }
            } else {
                NavigationStack(path: $path) {
                    ConversationListView(model: model) { path.append($0); wideSelection = $0 }
                        .navigationDestination(for: String.self) { route in
                            if let projectID = ConversationRoute.projectID(route) {
                                ProjectScreen(model: model, projectID: projectID) { path.append($0); wideSelection = $0 }
                            } else {
                                SessionRouteView(model: model, sessionID: route)
                            }
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
    var model: AgenthailIOSModel
    let sessionID: String

    private var session: SessionState? {
        if let value = model.session(sessionID) { return value }
        let cached = model.searchResults.first(where: { $0.id == sessionID })?.session ?? model.fetchedSubagents.session(sessionID)
        guard let detail = model.selectedDetail, detail.session.id == sessionID else { return cached }
        return detail.shown(over: cached, host: model.pairedHost(owning: sessionID))
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
