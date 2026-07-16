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
            VStack(spacing: 24) {
                Spacer()
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
                if model.pairing { ProgressView("Pairing securely") }
                Spacer()
                Text("Your conversations stay between this phone and your Mac over Tailscale.")
                    .font(.footnote).foregroundStyle(.secondary).multilineTextAlignment(.center).padding(.horizontal, 30)
            }
            .padding(.vertical)
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
    @State private var path: [String] = []

    var body: some View {
        NavigationStack(path: $path) {
            ConversationListView(model: model)
                .navigationDestination(for: String.self) { sessionID in
                    SessionRouteView(model: model, sessionID: sessionID)
                }
        }
        .onChange(of: model.requestedSessionID) { _, sessionID in
            guard let sessionID else { return }
            path = [sessionID]
            model.requestedSessionID = nil
        }
    }
}

struct SessionRouteView: View {
    @ObservedObject var model: AgenthailIOSModel
    let sessionID: String

    var body: some View {
        if let session = model.snapshot?.sessions.first(where: { $0.id == sessionID }) {
            SessionScreen(model: model, session: session)
        } else {
            ProgressView("Opening conversation")
                .task { await model.refresh(fresh: true) }
        }
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
                Text("Working now").font(.title2.bold())
                if model.currentSessions.isEmpty {
                    ContentUnavailableView("Nothing is running", systemImage: "checkmark.circle", description: Text("Current Claude Code and recent Codex work will appear here."))
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
                if (model.snapshot?.attention ?? []).isEmpty {
                    Text("You are all caught up.").foregroundStyle(.secondary).padding(.vertical, 8)
                } else {
                    ForEach(model.snapshot?.attention ?? []) { item in
                        VStack(alignment: .leading, spacing: 5) {
                            Text(item.target).fontWeight(.semibold)
                            Text(item.reason).foregroundStyle(.secondary)
                            Text(item.requestedAction).font(.caption.weight(.bold)).foregroundStyle(orange)
                        }
                        .padding().background(.thinMaterial, in: RoundedRectangle(cornerRadius: 16))
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
    @State private var showHistory = false
    @State private var search = ""

    private var sessions: [SessionState] {
        let source = showHistory ? (model.snapshot?.sessions ?? []) : model.currentSessions
        return search.isEmpty ? source : source.filter { $0.displayName.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        List {
            Picker("Scope", selection: $showHistory) {
                Text("Current").tag(false)
                Text("History").tag(true)
            }
            .pickerStyle(.segmented)
            ForEach(sessions) { session in
                NavigationLink(value: session.id) {
                    IOSSessionRow(session: session)
                }
            }
        }
        .listStyle(.plain)
        .searchable(text: $search, prompt: "Find a conversation")
        .navigationTitle("Conversations")
        .refreshable { await model.refresh(fresh: true) }
    }
}

struct SessionScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    @State private var showingCommands = false

    private var detail: SessionDetail? {
        model.selectedDetail?.session.id == session.id ? model.selectedDetail : nil
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 24) {
                    if detail?.transcriptTruncated == true {
                        Label("Older or oversized transcript content was shortened for this view.", systemImage: "text.badge.ellipsis")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                    if let context = detail?.context, context.contextWindow > 0 {
                        VStack(alignment: .leading, spacing: 6) {
                            ProgressView(value: context.fraction).tint(context.fraction > 0.85 ? orange : .green)
                            Text("\(context.usedTokens.formatted()) / \(context.contextWindow.formatted()) tokens").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                        }
                    }
                    ForEach(detail?.exchanges ?? []) { exchange in
                        if !exchange.user.isEmpty { IOSMessage(label: "YOU", text: exchange.user, color: orange) }
                        if !exchange.assistant.isEmpty { IOSMessage(label: session.surface == "claude" ? "CLAUDE CODE" : session.surface.uppercased(), text: exchange.assistant, color: .secondary) }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .padding()
            }
            .safeAreaInset(edge: .bottom) {
                if session.isReadOnly {
                    Label(session.readOnlyReason ?? "This conversation is read only.", systemImage: "lock").font(.footnote).foregroundStyle(.secondary).padding().frame(maxWidth: .infinity).background(.ultraThinMaterial)
                } else {
                    IOSComposer(model: model, session: session, showingCommands: $showingCommands)
                }
            }
            .navigationTitle(session.displayName)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        if session.capabilities.compact { Button("Compact", systemImage: "arrow.down.right.and.arrow.up.left") { model.action("compact", session: session) } }
                        if session.capabilities.interrupt && session.isWorking { Button("Stop", systemImage: "stop.fill", role: .destructive) { model.action("interrupt", session: session) } }
                    } label: { Image(systemName: "ellipsis.circle") }
                }
            }
            .task { await model.loadSession(session.id); proxy.scrollTo("bottom", anchor: .bottom) }
            .onChange(of: detail?.exchanges.count) { _, _ in proxy.scrollTo("bottom", anchor: .bottom) }
        }
    }
}

struct IOSComposer: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    @Binding var showingCommands: Bool

    var body: some View {
        VStack(spacing: 8) {
            if model.composer.hasPrefix("/") || showingCommands {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack {
                        if session.capabilities.compact { Button("/compact") { model.action("compact", session: session); model.composer = "" } }
                        if session.capabilities.interrupt { Button("/stop") { model.action("interrupt", session: session); model.composer = "" } }
                        if session.capabilities.model,
                           model.selectedDetail?.session.id == session.id,
                           let models = model.selectedDetail?.models {
                            Menu("/model") {
                                ForEach(models) { option in Button(option.displayName) { model.action("model", session: session, model: option.id); model.composer = "" } }
                            }
                        }
                    }.buttonStyle(.bordered)
                }
            }
            HStack(alignment: .bottom, spacing: 10) {
                TextField(session.isWorking ? "Steer this turn" : "Message this agent", text: $model.composer, axis: .vertical)
                    .lineLimit(1...6).textFieldStyle(.plain).padding(12).background(Color.secondary.opacity(0.12), in: RoundedRectangle(cornerRadius: 18))
                    .onChange(of: model.composer) { _, value in showingCommands = value.hasPrefix("/") }
                Button { model.send(to: session) } label: { Image(systemName: "arrow.up").font(.headline).frame(width: 38, height: 38) }
                    .buttonStyle(.borderedProminent).buttonBorderShape(.circle).tint(orange)
                    .disabled(model.composer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(.horizontal).padding(.vertical, 10).background(.ultraThinMaterial)
    }
}

struct IOSMessage: View {
    let label: String
    let text: String
    let color: Color
    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            Text(label).font(.caption.weight(.bold)).tracking(1.2).foregroundStyle(.secondary)
            if let attributed = try? AttributedString(markdown: text) { Text(attributed).textSelection(.enabled) } else { Text(text).textSelection(.enabled) }
        }
        .padding(.leading, 14).overlay(alignment: .leading) { Rectangle().fill(color.opacity(0.6)).frame(width: 2) }
    }
}

struct IOSSessionRow: View {
    let session: SessionState
    var body: some View {
        HStack(spacing: 12) {
            Circle().fill(session.isWorking ? orange : session.current ? .green : .secondary).frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 3) {
                Text(session.displayName).fontWeight(.semibold).lineLimit(1)
                Text("\(session.surface == "claude" ? "Claude Code" : session.surface.capitalized) · \(session.isWorking ? "Working" : session.currentReason?.capitalized ?? "Ready")").font(.subheadline).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 6)
    }
}

struct SettingsView: View {
    @ObservedObject var model: AgenthailIOSModel
    var body: some View {
        List {
            Section("Connection") {
                Label(model.connectionError == nil ? "Connected over Tailscale" : "Connection interrupted", systemImage: model.connectionError == nil ? "lock.shield.fill" : "wifi.exclamationmark")
                if let error = model.connectionError { Text(error).font(.footnote).foregroundStyle(.secondary) }
                Button("Reconnect") { model.connect() }
            }
            Section("Notifications") {
                LabeledContent("Status", value: model.notificationStatus)
                if model.notificationStatus == "Enabled" {
                    Button("Turn off notifications", role: .destructive) { model.turnOffNotifications() }
                } else {
                    Button("Enable notifications") { model.requestNotifications() }
                }
                Text("Completion and failure alerts are triggered by your Mac daemon.").font(.footnote).foregroundStyle(.secondary)
            }
            Section {
                Button("Disconnect this iPhone", role: .destructive) { model.unpair() }
            }
        }
        .navigationTitle("Settings")
    }
}
