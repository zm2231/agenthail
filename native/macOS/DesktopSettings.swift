import AppKit
import CoreImage.CIFilterBuiltins
import SwiftUI

struct DesktopSettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        TabView {
            GeneralSettings(model: model)
                .tabItem { Label("General", systemImage: "gearshape") }
            DevicesSettings(model: model)
                .tabItem { Label("Devices", systemImage: "iphone") }
            NotificationSettings(model: model)
                .tabItem { Label("Notifications", systemImage: "bell.badge") }
            DeliverySettings(model: model)
                .tabItem { Label("Delivery", systemImage: "arrow.triangle.branch") }
            ActivitySettings(model: model)
                .tabItem { Label("Activity", systemImage: "clock.arrow.circlepath") }
        }
        .frame(width: 620, height: 520)
        .task { await model.loadOperations() }
    }
}

private struct SettingsError: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        if let error = model.operationError {
            Label(error, systemImage: "exclamationmark.triangle")
                .font(.system(size: 12))
                .foregroundStyle(DesktopPalette.red)
                .textSelection(.enabled)
        }
    }
}

struct GeneralSettings: View {
    @ObservedObject var model: AgenthailModel
    @AppStorage("followUpDefault") private var followUpDefault = FollowUpAction.queue.rawValue
    @AppStorage(SpotlightIndex.preferenceKey) private var spotlightSessions = true

    var body: some View {
        Form {
            Section("Connection") {
                LabeledContent("Agenthail") {
                    Text(connectionLabel)
                        .foregroundStyle(model.isConnected ? DesktopPalette.green : DesktopPalette.amber)
                }
                if let pid = model.snapshot?.daemon.pid {
                    LabeledContent("Process", value: String(pid))
                }
                ForEach(model.snapshot?.surfaces ?? []) { surface in
                    LabeledContent(surface.name.capitalized) {
                        Text(surface.connected ? "Connected" : surface.healthDetail ?? surface.error ?? surface.health.capitalized)
                            .foregroundStyle(surface.connected ? DesktopPalette.text2 : DesktopPalette.amber)
                            .lineLimit(2)
                    }
                }
                HStack {
                    Button("Restart Agenthail") { model.restartDaemon() }
                    Button("Login Item Settings…") { _ = NativeCommand.run(["service", "settings"]) }
                }
            }
            Section("Sending while an agent works") {
                Picker("⌘↩", selection: $followUpDefault) {
                    Text("Queue the message (default)").tag(FollowUpAction.queue.rawValue)
                    Text("Steer the running turn").tag(FollowUpAction.steer.rawValue)
                }
                .pickerStyle(.radioGroup)
                Text("⌥⌘↩ always does the other one. Agents that can't be steered mid-turn send queued messages after the turn.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Section("Spotlight") {
                Toggle("Show sessions in Spotlight", isOn: $spotlightSessions)
                Text("Spotlight on this Mac can find sessions by name and folder. Choosing one opens it here.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
    }

    private var connectionLabel: String {
        if model.isConnected { return "Connected" }
        if model.daemonSlow { return "Slow to respond" }
        return model.connectionError ?? "Not running"
    }
}

struct DevicesSettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        Form {
            Section {
                LabeledContent("Phone access") {
                    if let remote = model.settings?.remoteAccess {
                        Toggle("", isOn: Binding(get: { remote.enabled }, set: { model.setRemoteAccess($0) }))
                            .labelsHidden()
                            .toggleStyle(.switch)
                    } else {
                        ProgressView().controlSize(.small)
                    }
                }
                if let remote = model.settings?.remoteAccess {
                    Text(remote.enabled ? "Reachable privately through Tailscale\(remote.dnsName.map { " at \($0)" } ?? "")." : remote.error ?? "Your iPhone can reach this Mac only while phone access is on.")
                        .font(.system(size: 12))
                        .foregroundStyle(remote.error == nil ? DesktopPalette.text2 : DesktopPalette.amber)
                }
            }
            Section("Pair an iPhone") {
                Button("Show Pairing Code") { model.createPairing() }
                    .disabled(model.settings?.remoteAccess.enabled != true)
                if let pairing = model.pairing {
                    PairingCode(pairing: pairing)
                }
            }
            Section("Paired devices") {
                if model.devices.isEmpty {
                    Text("No devices are paired.").foregroundStyle(DesktopPalette.text2)
                }
                ForEach(model.devices) { device in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(device.name)
                            Text([device.pushEnabled ? "Notifications on" : "Notifications off", device.lastSeenAt.map { "seen \(relativeAge($0))" }].compactMap { $0 }.joined(separator: " · "))
                                .font(.system(size: 12))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                        Spacer()
                        Button("Revoke", role: .destructive) { model.revokeDevice(device.id) }
                    }
                }
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
    }
}

private struct PairingCode: View {
    let pairing: PairingResponse

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            if let image = qrImage(pairing.pairingURL) {
                Image(nsImage: image).interpolation(.none).resizable().frame(width: 128, height: 128)
            }
            VStack(alignment: .leading, spacing: 6) {
                Text("Scan with Agenthail on iPhone")
                Text("The code expires in five minutes and works once.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
                Text(pairing.endpoint)
                    .font(.system(size: 11, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(2)
            }
        }
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

struct NotificationSettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        Form {
            Section {
                let status = model.settings?.notifications
                LabeledContent("Desktop notifications") {
                    if let status {
                        Toggle("", isOn: Binding(get: { status.enabled }, set: { model.updateNotifications($0 ? "notifications-enable" : "notifications-disable") }))
                            .labelsHidden()
                            .toggleStyle(.switch)
                    } else {
                        ProgressView().controlSize(.small)
                    }
                }
                Text(detail(status))
                    .font(.system(size: 12))
                    .foregroundStyle(status?.error == nil ? DesktopPalette.text2 : DesktopPalette.amber)
                HStack {
                    Button("Send Test Notification") { model.updateNotifications("notifications-test") }
                        .disabled(status?.enabled != true)
                    if status?.authorization == "denied" {
                        Button("Open System Settings…") { model.updateNotifications("notifications-settings") }
                    }
                }
            } footer: {
                Text("Agenthail notifies when an agent finishes or fails, and when a message can't be delivered.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
    }

    private func detail(_ status: NotificationStatusState?) -> String {
        guard let status else { return "Checking notification access…" }
        if let error = status.error, !error.isEmpty { return error }
        if status.authorization == "denied" { return "Blocked in System Settings." }
        return status.enabled ? "On. Alerts appear in Notification Center." : "Off."
    }
}

struct DeliverySettings: View {
    @ObservedObject var model: AgenthailModel
    @State private var relayFrom = ""
    @State private var relayTo = ""
    @State private var relayPattern = ".*"
    @State private var channelName = ""
    @State private var selectedChannel = ""
    @State private var channelTarget = ""
    @State private var channelMessage = ""

    private var sessions: [SessionState] { model.snapshot?.sessions ?? [] }
    private var writableSessions: [SessionState] { sessions.filter { !$0.isReadOnly } }

    var body: some View {
        Form {
            Section("Waiting to go out") {
                if (model.snapshot?.queue ?? []).isEmpty {
                    Text("No messages are waiting.").foregroundStyle(DesktopPalette.text2)
                }
                ForEach(model.snapshot?.queue ?? []) { item in
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(item.target)
                            Text(PeerEnvelope(item.message).summary)
                                .font(.system(size: 12))
                                .foregroundStyle(DesktopPalette.text2)
                                .lineLimit(2)
                        }
                        Spacer()
                        Text(item.status.capitalized)
                            .font(.system(size: 11))
                            .foregroundStyle(item.status == "dead" ? DesktopPalette.red : DesktopPalette.text2)
                        if item.status == "dead" {
                            Button("Retry") { model.perform(action: "queue-retry", queueID: item.id) }
                        }
                        Button("Cancel") { model.perform(action: "queue-cancel", queueID: item.id) }
                    }
                }
            }
            Section {
                ForEach(model.snapshot?.relays ?? []) { relay in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text("\(relay.from) → \(relay.to)")
                            Text(relay.pattern)
                                .font(.system(size: 11, design: .monospaced))
                                .foregroundStyle(DesktopPalette.text2)
                                .lineLimit(1)
                        }
                        Spacer()
                        Button("Remove") { model.performOperation(action: "relay-remove", relayID: relay.id) }
                    }
                }
                Picker("When", selection: $relayFrom) {
                    Text("Choose a session").tag("")
                    ForEach(sessions) { Text($0.displayName).tag($0.id) }
                }
                TextField("finishes with text matching", text: $relayPattern)
                Picker("hand off to", selection: $relayTo) {
                    Text("Choose a session").tag("")
                    ForEach(writableSessions) { Text($0.displayName).tag($0.id) }
                }
                Button("Add Handoff") {
                    model.performOperation(action: "relay-add", fromID: relayFrom, toID: relayTo, pattern: relayPattern)
                }
                .disabled(relayFrom.isEmpty || relayTo.isEmpty)
            } header: {
                Text("Automatic handoffs")
            } footer: {
                Text("When one session finishes with matching text, Agenthail sends its reply to another.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Section("Channels") {
                HStack {
                    TextField("New channel", text: $channelName)
                    Button("Create") {
                        model.performOperation(action: "channel-create", channel: channelName)
                        selectedChannel = channelName
                        channelName = ""
                    }
                    .disabled(channelName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
                Picker("Channel", selection: $selectedChannel) {
                    Text("Choose a channel").tag("")
                    ForEach(model.snapshot?.channels ?? []) { Text("#\($0.name)").tag($0.name) }
                }
                if let channel = model.snapshot?.channels.first(where: { $0.name == selectedChannel }) {
                    ForEach(channel.memberDetails ?? []) { member in
                        HStack {
                            Text(member.display).lineLimit(1)
                            Spacer()
                            Button("Remove") { model.performOperation(action: "channel-remove", channel: channel.name, targetID: member.id) }
                        }
                    }
                    if channel.memberDetails == nil && !channel.members.isEmpty {
                        Text(channel.members.joined(separator: ", ")).foregroundStyle(DesktopPalette.text2)
                    }
                    HStack {
                        Picker("Add", selection: $channelTarget) {
                            Text("Choose a session").tag("")
                            ForEach(writableSessions) { Text($0.displayName).tag($0.id) }
                        }
                        Button("Add") { model.performOperation(action: "channel-add", channel: selectedChannel, targetID: channelTarget) }
                            .disabled(channelTarget.isEmpty)
                    }
                    TextField("Message everyone in #\(channel.name)", text: $channelMessage, axis: .vertical)
                        .lineLimit(2...5)
                    HStack {
                        Button("Delete Channel", role: .destructive) { model.performOperation(action: "channel-delete", channel: selectedChannel) }
                        Spacer()
                        Button("Send") {
                            model.performOperation(action: "channel-send", message: channelMessage, channel: selectedChannel)
                            channelMessage = ""
                        }
                        .disabled(channelMessage.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                }
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
    }
}

struct ActivitySettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        Form {
            Section {
                HStack {
                    TextField("Search activity", text: $model.auditQuery)
                        .onSubmit { Task { await model.loadAudit(reset: true) } }
                    Picker("", selection: $model.auditKind) {
                        Text("All activity").tag("")
                        ForEach(model.auditKinds, id: \.self) { Text(label($0)).tag($0) }
                    }
                    .labelsHidden()
                    .frame(maxWidth: 170)
                    .onChange(of: model.auditKind) { Task { await model.loadAudit(reset: true) } }
                }
            }
            Section {
                if model.audit.isEmpty {
                    Text("No matching activity.").foregroundStyle(DesktopPalette.text2)
                }
                ForEach(model.audit) { entry in
                    VStack(alignment: .leading, spacing: 3) {
                        HStack {
                            Text(label(entry.kind))
                            Spacer()
                            Text(relativeAge(entry.createdAt))
                                .font(.system(size: 11))
                                .foregroundStyle(DesktopPalette.text2)
                                .help(entry.createdAt)
                        }
                        if let target = entry.target, !target.isEmpty {
                            Text(target).font(.system(size: 12)).foregroundStyle(DesktopPalette.text2)
                        }
                        if let message = entry.message, !message.isEmpty {
                            Text(PeerEnvelope(message).summary).font(.system(size: 12)).foregroundStyle(DesktopPalette.text2).lineLimit(2)
                        }
                        if let error = entry.error, !error.isEmpty {
                            Text(error).font(.system(size: 12)).foregroundStyle(DesktopPalette.red).lineLimit(3)
                        }
                    }
                }
                if model.auditHasMore {
                    Button("Load More") { Task { await model.loadAudit(reset: false) } }
                }
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
        .onAppear { model.operationsVisible = true }
        .onDisappear { model.operationsVisible = false }
    }

    private func label(_ kind: String) -> String {
        kind.replacingOccurrences(of: "_", with: " ").capitalized
    }
}
