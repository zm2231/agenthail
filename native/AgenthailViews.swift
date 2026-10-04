import AppKit
import CoreImage.CIFilterBuiltins
import SwiftUI

private let agenthailOrange = Color(red: 1, green: 0.37, blue: 0.16)

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
                ResponsivePairLayout(spacing: 24, minimumLeadingWidth: 340, minimumTrailingWidth: 340) {
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
                ResponsivePairLayout(spacing: 24, minimumLeadingWidth: 340, minimumTrailingWidth: 340) {
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
                ResponsivePairLayout(spacing: 24, minimumLeadingWidth: 340, minimumTrailingWidth: 340) {
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
                    AuditCard(model: model)
                }
            }
            .padding(38)
            .frame(maxWidth: 1500, alignment: .leading)
        }
        .task { await model.loadOperations() }
        .onAppear { model.operationsVisible = true }
        .onDisappear { model.operationsVisible = false }
    }

    private func notificationDetail(_ status: NotificationStatusState?) -> String {
        guard let status else { return "Checking notification access" }
        if let error = status.error, !error.isEmpty { return error }
        if status.enabled { return "Agent completions can appear in Notification Center" }
        if status.authorization == "denied" { return "Blocked in System Settings" }
        return "Enable alerts when an agent finishes"
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
