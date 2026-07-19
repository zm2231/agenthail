import AppKit
import CoreImage.CIFilterBuiltins
import SwiftUI

private let agenthailOrange = Color(red: 1, green: 0.37, blue: 0.16)

struct AgenthailRootView: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        NavigationSplitView {
            VStack(alignment: .leading, spacing: 18) {
                HStack(spacing: 12) {
                    AgenthailMark(size: 42)
                    Text("agenthail").font(.title2.weight(.bold))
                }
                .padding(.horizontal, 14)
                ForEach(AppSection.allCases) { section in
                    Button {
                        model.showSection(section)
                    } label: {
                        Label(section.rawValue, systemImage: section.symbol)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, 9)
                            .padding(.horizontal, 12)
                            .background(model.section == section ? agenthailOrange.opacity(0.14) : .clear, in: RoundedRectangle(cornerRadius: 10))
                    }
                    .buttonStyle(.plain)
                }
                Spacer()
                ConnectionStatus(model: model)
            }
            .padding(14)
            .navigationSplitViewColumnWidth(min: 190, ideal: 220, max: 250)
        } detail: {
            Group {
                switch model.section {
                case .overview: OverviewView(model: model)
                case .conversations: ConversationsView(model: model)
                case .operations: OperationsView(model: model)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(nsColor: .windowBackgroundColor))
        }
        .toolbar {
            ToolbarItem {
                Button { model.refreshCurrentSection() } label: { Image(systemName: "arrow.clockwise") }
                    .help("Refresh")
            }
        }
        .overlay(alignment: .top) {
            if let error = model.operationError {
                ErrorBanner(message: error) { model.operationError = nil }
                    .padding(.top, 8)
            }
        }
    }
}

struct OverviewView: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 34) {
                PageHeader(eyebrow: "TODAY", title: "Your agents, in one place.", subtitle: model.isConnected ? "Private and connected" : "Connection interrupted")
                if !model.isConnected {
                    RecoveryCard(model: model)
                }
                ViewThatFits(in: .horizontal) {
                    HStack(alignment: .top, spacing: 38) {
                        currentConversations
                        surfaces
                    }
                    VStack(alignment: .leading, spacing: 34) {
                        currentConversations
                        surfaces
                    }
                }
                AttentionSection(model: model)
            }
            .padding(38)
            .frame(maxWidth: 1500, alignment: .leading)
        }
    }

    private var currentConversations: some View {
        VStack(alignment: .leading, spacing: 14) {
            SectionHeader(eyebrow: "WORKING NOW", title: "Current conversations")
            if model.currentSessions.isEmpty {
                EmptyState(title: "Nothing is running", detail: "Open Claude Code or use Codex and active work will appear here.")
            } else {
                ForEach(model.currentSessions) { session in
                    SessionRow(session: session) { model.selectSession(session.id) }
                }
            }
        }
        .frame(minWidth: 320, maxWidth: .infinity, alignment: .topLeading)
    }

    private var surfaces: some View {
        VStack(alignment: .leading, spacing: 14) {
            SectionHeader(eyebrow: "CONNECTIONS", title: "Your surfaces")
            ForEach(model.snapshot?.surfaces ?? []) { surface in
                SurfaceRow(surface: surface, sessions: model.snapshot?.sessions ?? [], queue: model.snapshot?.queue ?? [])
            }
        }
        .frame(minWidth: 360, maxWidth: .infinity, alignment: .topLeading)
    }
}

struct ConversationsView: View {
    @ObservedObject var model: AgenthailModel
    @State private var history = false
    @State private var search = ""

    private var sessions: [SessionState] {
        let source = history ? (model.snapshot?.sessions ?? []) : model.currentSessions
        guard !search.isEmpty else { return source }
        return source.filter { $0.displayName.localizedCaseInsensitiveContains(search) || $0.surface.localizedCaseInsensitiveContains(search) }
    }

    var body: some View {
        GeometryReader { geometry in
            if geometry.size.width >= 820 {
                HSplitView {
                    conversationList
                    ConversationDetailView(model: model)
                        .frame(minWidth: 440)
                }
            } else if model.conversationListVisible {
                conversationList
            } else {
                VStack(spacing: 0) {
                    HStack {
                        Button { model.showConversationList() } label: {
                            Label("Conversations", systemImage: "chevron.left")
                        }
                        .buttonStyle(.plain)
                        Spacer()
                    }
                    .padding(.horizontal, 18)
                    .padding(.vertical, 12)
                    Divider()
                    ConversationDetailView(model: model)
                }
            }
        }
    }

    private var conversationList: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("Conversations").font(.title2.weight(.bold))
                Spacer()
                Text("\(sessions.count)").foregroundStyle(.secondary)
            }
            Picker("Conversation scope", selection: $history) {
                Text("Current").tag(false)
                Text("History").tag(true)
            }
            .pickerStyle(.segmented)
            TextField("Find a conversation", text: $search)
                .textFieldStyle(.roundedBorder)
            ScrollView {
                LazyVStack(spacing: 4) {
                    ForEach(sessions) { session in
                        SessionRow(session: session, selected: session.id == model.selectedSessionID) { model.selectSession(session.id) }
                    }
                }
            }
        }
        .padding(20)
        .frame(minWidth: 250, idealWidth: 310, maxWidth: 370)
    }
}

struct ConversationDetailView: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        if let session = model.selectedSession {
            VStack(spacing: 0) {
                ConversationHeader(model: model, session: session)
                Divider()
                if let detail = model.detail {
                    TranscriptView(detail: detail)
                    if !detail.readOnly {
                        ComposerView(model: model, detail: detail)
                    } else {
                        HStack(spacing: 8) {
                            Image(systemName: "lock")
                            Text(detail.readOnlyReason)
                        }
                        .foregroundStyle(.secondary)
                        .padding(18)
                    }
                } else {
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
        } else {
            VStack(spacing: 12) {
                Image(systemName: "bubble.left.and.bubble.right").font(.system(size: 36)).foregroundStyle(.secondary)
                Text("Choose a conversation").font(.title2.weight(.semibold))
                Text("Current work and recent history appear in the sidebar.").foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

struct ConversationHeader: View {
    @ObservedObject var model: AgenthailModel
    let session: SessionState

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(session.surface == "claude" ? "CLAUDE CODE" : session.surface.uppercased())
                        .font(.caption.weight(.bold)).tracking(1.5).foregroundStyle(.secondary)
                    Text(session.displayName).font(.title.weight(.bold)).lineLimit(2)
                    Text(statusLine).foregroundStyle(.secondary)
                }
                Spacer()
                if session.capabilities.interrupt && session.isWorking {
                    Button("Stop") { model.perform(action: "interrupt", sessionID: session.id) }
                }
                if session.capabilities.compact {
                    Button("Compact") { model.perform(action: "compact", sessionID: session.id) }
                }
            }
            if let context = model.detail?.context, context.contextWindow > 0 {
                HStack(spacing: 10) {
                    ProgressView(value: context.fraction).tint(context.fraction > 0.85 ? agenthailOrange : .accentColor)
                    Text(context.compacting ? "Compacting context" : "\(Int(context.fraction * 100))% · \(context.usedTokens.formatted()) / \(context.contextWindow.formatted())")
                        .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
        }
        .padding(24)
    }

    private var statusLine: String {
        let state = session.isWorking ? "Working" : session.open ? "Open" : "Ready"
        return "\(state) · \(session.queueCount) queued"
    }
}

struct TranscriptView: View {
    let detail: SessionDetail

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 30) {
                    if detail.transcriptTruncated == true {
                        Label("Older or oversized transcript content was shortened for this view.", systemImage: "text.badge.ellipsis")
                            .font(.callout).foregroundStyle(.secondary)
                    }
                    ForEach(detail.exchanges) { exchange in
                        if !exchange.user.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                            TranscriptMessage(label: "YOU", text: exchange.user, accent: agenthailOrange)
                        }
                        if !exchange.assistant.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                            TranscriptMessage(label: detail.session.surface == "claude" ? "CLAUDE CODE" : detail.session.surface.uppercased(), text: exchange.assistant, accent: .secondary)
                        }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .padding(.horizontal, 44)
                .padding(.vertical, 30)
                .frame(maxWidth: 980, alignment: .leading)
                .frame(maxWidth: .infinity)
            }
            .onChange(of: detail.exchanges.count) { _ in proxy.scrollTo("bottom", anchor: .bottom) }
        }
    }
}

struct TranscriptMessage: View {
    let label: String
    let text: String
    let accent: Color

    var body: some View {
        VStack(alignment: .leading, spacing: 9) {
            Text(label).font(.caption.weight(.bold)).tracking(1.4).foregroundStyle(.secondary)
            MarkdownText(text: text)
                .textSelection(.enabled)
                .font(.body)
                .lineSpacing(5)
        }
        .padding(.leading, 18)
        .overlay(alignment: .leading) { Rectangle().fill(accent.opacity(0.55)).frame(width: 2) }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct MarkdownText: View {
    let text: String
    var body: some View {
        if let attributed = try? AttributedString(markdown: text, options: .init(interpretedSyntax: .full)) {
            Text(attributed)
        } else {
            Text(text)
        }
    }
}

struct ComposerView: View {
    @ObservedObject var model: AgenthailModel
    let detail: SessionDetail
    @State private var showCommands = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if showCommands || model.composer.hasPrefix("/") {
                CommandPalette(model: model, detail: detail) { showCommands = false }
            }
            HStack(alignment: .bottom, spacing: 10) {
                TextEditor(text: $model.composer)
                    .font(.body)
                    .scrollContentBackground(.hidden)
                    .frame(minHeight: 38, maxHeight: 100)
                    .padding(8)
                    .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 14))
                    .onChange(of: model.composer) { value in showCommands = value.hasPrefix("/") }
                Button {
                    model.send()
                } label: {
                    Image(systemName: "arrow.up").font(.headline).frame(width: 34, height: 34)
                }
                .buttonStyle(.borderedProminent)
                .tint(agenthailOrange)
                .disabled(model.composer.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            Text(detail.session.status == "busy" ? "Sends guidance into the active turn" : "Message this agent")
                .font(.caption).foregroundStyle(.secondary)
        }
        .padding(16)
        .background(.ultraThinMaterial)
    }
}

struct CommandPalette: View {
    @ObservedObject var model: AgenthailModel
    let detail: SessionDetail
    let dismiss: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            if detail.capabilities.compact {
                Button("/compact") { run("compact") }
            }
            if detail.capabilities.interrupt {
                Button("/stop") { run("interrupt") }
            }
            if detail.capabilities.model, let models = detail.models, !models.isEmpty {
                Menu("/model") {
                    ForEach(models) { option in
                        Button(option.displayName) {
                            model.perform(action: "model", sessionID: detail.session.id, model: option.id)
                            model.composer = ""
                            dismiss()
                        }
                    }
                }
            }
        }
        .buttonStyle(.borderless)
    }

    private func run(_ action: String) {
        model.perform(action: action, sessionID: detail.session.id)
        model.composer = ""
        dismiss()
    }
}

struct OperationsView: View {
    @ObservedObject var model: AgenthailModel
    @State private var relayFrom = ""
    @State private var relayTo = ""
    @State private var relayPattern = ".*"
    @State private var channelName = ""
    @State private var selectedChannel = ""
    @State private var channelTarget = ""
    @State private var channelMessage = ""

    private var writableSessions: [SessionState] {
        (model.snapshot?.sessions ?? []).filter { !$0.isReadOnly }
    }

    private var observableSessions: [SessionState] {
        model.snapshot?.sessions ?? []
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 34) {
                PageHeader(eyebrow: "OPERATIONS", title: "Manage Agenthail", subtitle: "Delivery, devices, and automation")
                ResponsivePair {
                    OperationsCard(title: "Waiting to go out", symbol: "tray.full") {
                        if (model.snapshot?.queue ?? []).isEmpty {
                            Text("No messages are waiting.").foregroundStyle(.secondary)
                        } else {
                            ForEach(model.snapshot?.queue ?? []) { item in
                                VStack(alignment: .leading, spacing: 6) {
                                    HStack {
                                        Text(item.target).fontWeight(.semibold)
                                        Spacer()
                                        Text(item.status.uppercased()).font(.caption.weight(.bold)).foregroundStyle(.secondary)
                                    }
                                    Text(item.message).lineLimit(3).foregroundStyle(.secondary)
                                    HStack {
                                        if item.status == "dead" {
                                            Button("Retry") { model.perform(action: "queue-retry", queueID: item.id) }
                                        }
                                        Button("Cancel") { model.perform(action: "queue-cancel", queueID: item.id) }
                                            .buttonStyle(.borderless)
                                    }
                                }
                                .padding(.vertical, 8)
                                Divider()
                            }
                        }
                    }
                } trailing: {
                    OperationsCard(title: "Connected devices", symbol: "iphone.and.arrow.forward") {
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text("Private phone access").fontWeight(.semibold)
                                if let remote = model.settings?.remoteAccess {
                                    Text(remote.enabled ? "Connected through Tailscale" : remote.error ?? "Not enabled")
                                        .font(.caption).foregroundStyle(.secondary).lineLimit(2)
                                } else {
                                    Text("Checking Tailscale").font(.caption).foregroundStyle(.secondary)
                                }
                            }
                            Spacer()
                            if model.settings?.remoteAccess.enabled == true {
                                Button("Turn off") { model.setRemoteAccess(false) }.buttonStyle(.borderless)
                            } else {
                                Button("Enable") { model.setRemoteAccess(true) }.buttonStyle(.bordered)
                            }
                        }
                        Divider()
                        Button("Pair an iPhone") { model.createPairing() }
                            .buttonStyle(.borderedProminent)
                            .tint(agenthailOrange)
                            .disabled(model.settings?.remoteAccess.enabled != true)
                        if let pairing = model.pairing {
                            PairingView(pairing: pairing)
                        }
                        ForEach(model.devices) { device in
                            HStack {
                                VStack(alignment: .leading) {
                                    Text(device.name).fontWeight(.semibold)
                                    Text(device.pushEnabled ? "Notifications on" : "Notifications off").font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                Button("Revoke") { model.revokeDevice(device.id) }
                                    .buttonStyle(.borderless)
                            }
                            Divider()
                        }
                    }
                }
                ResponsivePair {
                    OperationsCard(title: "Automatic handoffs", symbol: "arrow.triangle.branch") {
                        Picker("From", selection: $relayFrom) {
                            Text("Choose a source").tag("")
                            ForEach(observableSessions) { session in Text(session.displayName).tag(session.id) }
                        }
                        Picker("To", selection: $relayTo) {
                            Text("Choose a destination").tag("")
                            ForEach(writableSessions) { session in Text(session.displayName).tag(session.id) }
                        }
                        TextField("Completion pattern", text: $relayPattern)
                        HStack {
                            Spacer()
                            Button("Add handoff") {
                                model.performOperation(action: "relay-add", fromID: relayFrom, toID: relayTo, pattern: relayPattern)
                            }
                            .buttonStyle(.borderedProminent)
                            .tint(agenthailOrange)
                            .disabled(relayFrom.isEmpty || relayTo.isEmpty)
                        }
                        Divider()
                        if (model.snapshot?.relays ?? []).isEmpty {
                            Text("No automatic handoffs configured.").foregroundStyle(.secondary)
                        } else {
                            ForEach(model.snapshot?.relays ?? []) { relay in
                                HStack {
                                    VStack(alignment: .leading, spacing: 3) {
                                        Text("\(relay.from) → \(relay.to)").fontWeight(.semibold)
                                        Text(relay.pattern).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1)
                                    }
                                    Spacer(minLength: 12)
                                    Button("Remove") { model.performOperation(action: "relay-remove", relayID: relay.id) }
                                        .buttonStyle(.borderless)
                                }
                                .padding(.vertical, 4)
                            }
                        }
                    }
                } trailing: {
                    OperationsCard(title: "Desktop notifications", symbol: "bell.badge") {
                        let status = model.settings?.notifications
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text(status?.enabled == true ? "Enabled" : "Not enabled").fontWeight(.semibold)
                                Text(notificationDetail(status)).font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                            if status?.enabled == true {
                                Button("Test") { model.updateNotifications("notifications-test") }
                                Button("Turn off") { model.updateNotifications("notifications-disable") }
                                    .buttonStyle(.borderless)
                            } else {
                                Button("Enable") { model.updateNotifications("notifications-enable") }
                                    .buttonStyle(.borderedProminent).tint(agenthailOrange)
                            }
                        }
                        if status?.authorization == "denied" {
                            Button("Open System Settings") { model.updateNotifications("notifications-settings") }
                                .buttonStyle(.borderless)
                        }
                    }
                }
                ResponsivePair {
                    OperationsCard(title: "Shared handoffs", symbol: "person.3") {
                        HStack {
                            TextField("Channel name", text: $channelName)
                            Button("Create") {
                                model.performOperation(action: "channel-create", channel: channelName)
                                selectedChannel = channelName
                                channelName = ""
                            }
                            .disabled(channelName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }
                        Divider()
                        Picker("Channel", selection: $selectedChannel) {
                            Text("Choose a channel").tag("")
                            ForEach(model.snapshot?.channels ?? []) { channel in Text("#\(channel.name)").tag(channel.name) }
                        }
                        Picker("Agent", selection: $channelTarget) {
                            Text("Choose an agent").tag("")
                            ForEach(writableSessions) { session in Text(session.displayName).tag(session.id) }
                        }
                        HStack {
                            Button("Add agent") { model.performOperation(action: "channel-add", channel: selectedChannel, targetID: channelTarget) }
                                .disabled(selectedChannel.isEmpty || channelTarget.isEmpty)
                            Spacer()
                            Button("Delete channel", role: .destructive) { model.performOperation(action: "channel-delete", channel: selectedChannel) }
                                .buttonStyle(.borderless)
                                .disabled(selectedChannel.isEmpty)
                        }
                        if let channel = model.snapshot?.channels.first(where: { $0.name == selectedChannel }) {
                            ForEach(channel.memberDetails ?? []) { member in
                                HStack {
                                    Text(member.display).lineLimit(1)
                                    Spacer()
                                    Button("Remove") { model.performOperation(action: "channel-remove", channel: channel.name, targetID: member.id) }
                                        .buttonStyle(.borderless)
                                }
                            }
                            if channel.memberDetails == nil && !channel.members.isEmpty {
                                Text(channel.members.joined(separator: ", ")).foregroundStyle(.secondary)
                            }
                        }
                        Divider()
                        TextField("Message everyone in this channel", text: $channelMessage, axis: .vertical)
                            .lineLimit(2...5)
                        HStack {
                            Spacer()
                            Button("Send") {
                                model.performOperation(action: "channel-send", message: channelMessage, channel: selectedChannel)
                                channelMessage = ""
                            }
                            .buttonStyle(.borderedProminent).tint(agenthailOrange)
                            .disabled(selectedChannel.isEmpty || channelMessage.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        }
                    }
                } trailing: {
                    AuditCard(model: model)
                }
            }
            .padding(38)
            .frame(maxWidth: 1500, alignment: .leading)
        }
        .task { await model.loadOperations() }
    }

    private func notificationDetail(_ status: NotificationStatusState?) -> String {
        guard let status else { return "Checking notification access" }
        if let error = status.error, !error.isEmpty { return error }
        if status.enabled { return "Agent completions can appear in Notification Center" }
        if status.authorization == "denied" { return "Blocked in System Settings" }
        return "Enable alerts when an agent finishes"
    }
}

struct ResponsivePair<Leading: View, Trailing: View>: View {
    @ViewBuilder let leading: Leading
    @ViewBuilder let trailing: Trailing

    init(@ViewBuilder leading: () -> Leading, @ViewBuilder trailing: () -> Trailing) {
        self.leading = leading()
        self.trailing = trailing()
    }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(alignment: .top, spacing: 24) {
                leading.frame(minWidth: 340)
                trailing.frame(minWidth: 340)
            }
            VStack(alignment: .leading, spacing: 24) {
                leading
                trailing
            }
        }
    }
}

struct AuditCard: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        OperationsCard(title: "Audit trail", symbol: "clock.arrow.circlepath") {
            HStack {
                TextField("Search activity", text: $model.auditQuery)
                    .onSubmit { Task { await model.loadAudit(reset: true) } }
                Picker("Kind", selection: $model.auditKind) {
                    Text("All activity").tag("")
                    ForEach(model.auditKinds, id: \.self) { kind in Text(kind.replacingOccurrences(of: "_", with: " ").capitalized).tag(kind) }
                }
                .frame(maxWidth: 180)
                .onChange(of: model.auditKind) { _ in Task { await model.loadAudit(reset: true) } }
            }
            if model.audit.isEmpty {
                Text("No matching activity.").foregroundStyle(.secondary)
            }
            ForEach(model.audit) { entry in
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(entry.kind.replacingOccurrences(of: "_", with: " ").capitalized).fontWeight(.semibold)
                        Spacer()
                        Text(entry.createdAt).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                    if let target = entry.target, !target.isEmpty { Text(target).foregroundStyle(.secondary) }
                    if let message = entry.message, !message.isEmpty { Text(message).foregroundStyle(.secondary).lineLimit(2) }
                    if let error = entry.error, !error.isEmpty { Text(error).foregroundStyle(.red).lineLimit(3) }
                }
                .padding(.vertical, 6)
                Divider()
            }
            if model.auditHasMore {
                Button("Load more") { Task { await model.loadAudit(reset: false) } }
                    .frame(maxWidth: .infinity)
            }
        }
    }
}

struct PairingView: View {
    let pairing: PairingResponse

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            if let image = qrImage(pairing.pairingURL) {
                Image(nsImage: image).interpolation(.none).resizable().frame(width: 128, height: 128)
            }
            VStack(alignment: .leading, spacing: 6) {
                Text("Scan with Agenthail on iPhone").fontWeight(.semibold)
                Text("The code expires in five minutes and can be used once.").font(.caption).foregroundStyle(.secondary)
                Text(pairing.endpoint).font(.caption.monospaced()).textSelection(.enabled).lineLimit(2)
            }
        }
        .padding(12)
        .background(.quaternary.opacity(0.35), in: RoundedRectangle(cornerRadius: 12))
    }

    private func qrImage(_ value: String) -> NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(value.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)) else { return nil }
        let representation = NSCIImageRep(ciImage: output)
        let image = NSImage(size: representation.size)
        image.addRepresentation(representation)
        return image
    }
}

struct OperationsCard<Content: View>: View {
    let title: String
    let symbol: String
    @ViewBuilder let content: Content

    init(title: String, symbol: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.symbol = symbol
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label(title, systemImage: symbol).font(.title3.weight(.bold))
            content
        }
        .padding(22)
        .frame(maxWidth: .infinity, alignment: .topLeading)
        .background(.quaternary.opacity(0.22), in: RoundedRectangle(cornerRadius: 18))
    }
}

struct AttentionSection: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            SectionHeader(eyebrow: "ATTENTION", title: "Needs you")
            if (model.snapshot?.attention ?? []).isEmpty {
                EmptyState(title: "You are all caught up", detail: "Agenthail only puts something here when it can prove a decision or recovery action is needed.")
            } else {
                ForEach(model.snapshot?.attention ?? []) { item in
                    Button { model.selectSession(item.sessionId) } label: {
                        HStack {
                            Image(systemName: "exclamationmark.circle.fill").foregroundStyle(agenthailOrange)
                            VStack(alignment: .leading) {
                                Text(item.target).fontWeight(.semibold)
                                Text(item.reason).foregroundStyle(.secondary)
                            }
                            Spacer()
                            Text(item.requestedAction).font(.caption.weight(.bold)).foregroundStyle(.secondary)
                        }
                        .padding(14)
                    }
                    .buttonStyle(.plain)
                    .background(.quaternary.opacity(0.25), in: RoundedRectangle(cornerRadius: 12))
                }
            }
        }
    }
}

struct SurfaceRow: View {
    let surface: SurfaceState
    let sessions: [SessionState]
    let queue: [QueueState]

    var body: some View {
        HStack(spacing: 14) {
            SurfaceMark(name: surface.name)
            VStack(alignment: .leading, spacing: 4) {
                Text(surfaceName).font(.headline)
                HStack(spacing: 6) {
                    Circle().fill(surface.connected ? Color.green : Color.secondary).frame(width: 7, height: 7)
                    Text(surface.connected ? "Connected" : "Not connected").foregroundStyle(.secondary)
                }
            }
            Spacer()
            Metric(label: "WORKING", value: sessions.filter { $0.surface == surface.name && $0.isWorking }.count)
            Metric(label: surface.name == "claude" ? "OPEN" : "RECENT", value: sessions.filter { $0.surface == surface.name && $0.current }.count)
            Metric(label: "QUEUED", value: queuedCount)
        }
        .padding(.vertical, 14)
    }

    private var surfaceName: String {
        switch surface.name {
        case "claude": return "Claude Code"
        case "codex": return "Codex"
        case "notion": return "Notion"
        default: return surface.name.capitalized
        }
    }

    private var queuedCount: Int {
        let ids = Set(sessions.lazy.filter { $0.surface == surface.name }.map(\.id))
        return queue.filter { ids.contains($0.sessionId) }.count
    }
}

struct SessionRow: View {
    let session: SessionState
    var selected = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 11) {
                Circle().fill(session.isWorking ? agenthailOrange : session.current ? Color.green : Color.secondary).frame(width: 8, height: 8)
                VStack(alignment: .leading, spacing: 3) {
                    Text(session.displayName).fontWeight(.semibold).lineLimit(1).truncationMode(.tail)
                    Text("\(surfaceName)  \(session.isWorking ? "Working" : session.currentReason?.capitalized ?? "Ready")")
                        .font(.caption).foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(selected ? agenthailOrange.opacity(0.14) : .clear, in: RoundedRectangle(cornerRadius: 10))
            .overlay(alignment: .leading) {
                if selected { RoundedRectangle(cornerRadius: 2).fill(agenthailOrange).frame(width: 3).padding(.vertical, 7) }
            }
        }
        .buttonStyle(.plain)
    }

    private var surfaceName: String { session.surface == "claude" ? "Claude Code" : session.surface.capitalized }
}

struct Metric: View {
    let label: String
    let value: Int
    var body: some View {
        VStack(alignment: .trailing, spacing: 3) {
            Text(label).font(.caption2.weight(.bold)).tracking(1).foregroundStyle(.secondary)
            Text(value.formatted()).font(.title3.weight(.semibold).monospacedDigit())
        }
        .frame(minWidth: 58, alignment: .trailing)
    }
}

struct PageHeader: View {
    let eyebrow: String
    let title: String
    let subtitle: String
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(eyebrow).font(.caption.weight(.bold)).tracking(1.8).foregroundStyle(.secondary)
            Text(title).font(.system(size: 44, weight: .bold, design: .rounded))
            Text(subtitle).font(.title3).foregroundStyle(.secondary)
        }
    }
}

struct SectionHeader: View {
    let eyebrow: String
    let title: String
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(eyebrow).font(.caption.weight(.bold)).tracking(1.6).foregroundStyle(.secondary)
            Text(title).font(.title2.weight(.bold))
        }
    }
}

struct EmptyState: View {
    let title: String
    let detail: String
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).fontWeight(.semibold)
            Text(detail).foregroundStyle(.secondary)
        }
        .padding(18)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.18), in: RoundedRectangle(cornerRadius: 12))
    }
}

struct RecoveryCard: View {
    @ObservedObject var model: AgenthailModel
    var body: some View {
        HStack {
            Image(systemName: "bolt.horizontal.circle").font(.title2).foregroundStyle(agenthailOrange)
            VStack(alignment: .leading) {
                Text("Agenthail is not connected").fontWeight(.semibold)
                Text(model.connectionError ?? "Agenthail could not connect.").foregroundStyle(.secondary)
            }
            Spacer()
            Button("Restart") { model.restartDaemon() }.buttonStyle(.borderedProminent).tint(agenthailOrange)
        }
        .padding(18)
        .background(agenthailOrange.opacity(0.1), in: RoundedRectangle(cornerRadius: 14))
    }
}

struct ConnectionStatus: View {
    @ObservedObject var model: AgenthailModel
    var body: some View {
        HStack(spacing: 8) {
            Circle().fill(model.isConnected ? Color.green : agenthailOrange).frame(width: 8, height: 8)
            Text(model.isConnected ? "Connected" : "Not connected").font(.caption).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
    }
}

struct ErrorBanner: View {
    let message: String
    let dismiss: () -> Void
    var body: some View {
        HStack {
            Image(systemName: "exclamationmark.triangle.fill")
            Text(message).lineLimit(2)
            Button(action: dismiss) { Image(systemName: "xmark") }.buttonStyle(.plain)
        }
        .padding(.horizontal, 14).padding(.vertical, 9)
        .background(.red.opacity(0.9), in: Capsule())
        .foregroundStyle(.white)
    }
}

struct AgenthailMark: View {
    let size: CGFloat
    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.26).fill(agenthailOrange)
            Text("A").font(.system(size: size * 0.48, weight: .black)).foregroundStyle(.black)
        }
        .frame(width: size, height: size)
    }
}

struct SurfaceMark: View {
    let name: String
    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 12).fill(.quaternary.opacity(0.35))
            Text(name == "claude" ? "✦" : name == "codex" ? "◇" : "N").font(.title3.weight(.bold)).foregroundStyle(name == "claude" ? agenthailOrange : .primary)
        }
        .frame(width: 44, height: 44)
    }
}
