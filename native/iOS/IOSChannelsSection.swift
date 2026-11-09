import SwiftUI

struct IOSChannelsSection: View {
    var model: AgenthailIOSModel
    let hostID: String
    let channels: [ChannelState]
    let busy: Bool
    let run: (@escaping @MainActor () async throws -> Void) async -> Bool
    @State private var newName = ""
    @State private var adding: [String: String] = [:]
    @State private var messages: [String: String] = [:]
    @State private var keys: [String: String] = [:]
    @State private var sent: [String: String] = [:]
    @State private var deleting: ChannelState?

    var body: some View {
        Section("Channels") {
            if channels.isEmpty {
                Text("No shared channels yet.").foregroundStyle(PhonePalette.muted)
            }
            HStack {
                TextField("New channel", text: $newName)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .accessibilityIdentifier("new-channel")
                Button("Create") {
                    let name = newName.trimmingCharacters(in: .whitespacesAndNewlines)
                    Task { if await run({ try await model.groupAction(on: hostID, "channel-create", channel: name) }) { newName = "" } }
                }
                .disabled(busy || newName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        ForEach(channels) { channel in
            Section("#\(channel.name)") { rows(channel) }
        }
        .confirmationDialog("Delete #\(deleting?.name ?? "")?", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }), titleVisibility: .visible, presenting: deleting) { channel in
            Button("Delete Channel", role: .destructive) {
                Task { _ = await run { try await model.groupAction(on: hostID, "channel-delete", channel: channel.name) } }
            }
        } message: { _ in
            Text("Its members stay; only the channel goes.")
        }
    }

    @ViewBuilder private func rows(_ channel: ChannelState) -> some View {
        ForEach(channel.memberDetails ?? channel.members.map { ChannelMemberState(id: $0, display: $0) }) { member in
            Text(model.groupName(member.id, on: hostID) == member.id ? member.display : model.groupName(member.id, on: hostID))
                .swipeActions {
                    Button("Remove", role: .destructive) {
                        Task { _ = await run { try await model.groupAction(on: hostID, "channel-remove", channel: channel.name, targetID: member.id) } }
                    }
                    .disabled(busy)
                }
        }
        IOSGroupSessionPicker(model: model, title: "Add", hostID: hostID, writableOnly: true, selection: Binding(get: { adding[channel.name] ?? "" }, set: { adding[channel.name] = $0 }))
        if let target = adding[channel.name], !target.isEmpty {
            Button("Add to #\(channel.name)") {
                Task { if await run({ try await model.groupAction(on: hostID, "channel-add", channel: channel.name, targetID: target) }) { adding[channel.name] = nil } }
            }
            .disabled(busy)
        }
        HStack(alignment: .bottom) {
            TextField("Message everyone in #\(channel.name)", text: Binding(get: { messages[channel.name] ?? "" }, set: { typed in
                guard typed != (messages[channel.name] ?? "") else { return }
                messages[channel.name] = typed
                keys[channel.name] = nil
                sent[channel.name] = nil
            }), axis: .vertical)
                .lineLimit(2...5)
                .disabled(busy)
                .accessibilityIdentifier("channel-message-" + channel.name)
            Button("Send") { Task { await send(channel.name) } }
                .buttonStyle(.borderless)
                .disabled(busy || (messages[channel.name] ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("channel-send-" + channel.name)
        }
        .alignmentGuide(.listRowSeparatorLeading) { _ in 0 }
        if let notice = sent[channel.name] {
            Text(notice).font(.footnote).foregroundStyle(PhonePalette.muted).accessibilityIdentifier("channel-sent-" + channel.name)
        }
        Button("Delete Channel", role: .destructive) { deleting = channel }
            .disabled(busy)
    }

    private func send(_ name: String) async {
        let key = keys[name] ?? UUID().uuidString
        keys[name] = key
        let text = messages[name] ?? ""
        var notice: String?
        if await run({ notice = try await model.sendToChannel(name, on: hostID, message: text, idempotencyKey: key) }) {
            messages[name] = nil
            keys[name] = nil
            sent[name] = notice
        }
    }
}
