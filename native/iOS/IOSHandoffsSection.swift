import SwiftUI

struct IOSHandoffsSection: View {
    var model: AgenthailIOSModel
    let hostID: String
    let relays: [RelayState]
    let busy: Bool
    let run: (@escaping @MainActor () async throws -> Void) async -> Bool
    @State private var from = ""
    @State private var to = ""
    @State private var pattern = ".*"
    @State private var once = false
    @State private var handoff = "turn_finished"

    var body: some View {
        Section {
            if relays.isEmpty {
                Text("No automatic handoffs yet.").foregroundStyle(PhonePalette.muted)
            }
            ForEach(relays) { relay in
                VStack(alignment: .leading, spacing: 4) {
                    Text("\(model.groupName(relay.from, on: hostID)) → \(model.groupName(relay.to, on: hostID))")
                    Text(relay.pattern).font(.footnote.monospaced()).foregroundStyle(PhonePalette.muted)
                    Text(RelayFacts.state(relay)).font(.footnote).foregroundStyle(PhonePalette.muted)
                    if let error = relay.lastError, !error.isEmpty {
                        Text(error).font(.footnote).foregroundStyle(PhonePalette.failed)
                    }
                }
                .swipeActions {
                    Button("Remove", role: .destructive) {
                        Task { _ = await run { try await model.groupAction(on: hostID, "relay-remove", relayID: relay.id) } }
                    }
                    .disabled(busy)
                }
            }
        } header: {
            Text("Automatic handoffs · \(relays.count)")
        }
        Section {
            IOSGroupSessionPicker(model: model, title: "When", hostID: hostID, selection: $from)
            Picker("Hand off when", selection: $handoff) {
                Text("Turn finished").tag("turn_finished")
                Text("Goal completed").tag("goal_completed")
            }
            LabeledContent("Matching") {
                TextField("Text pattern", text: $pattern)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .font(.body.monospaced())
                    .multilineTextAlignment(.trailing)
            }
            IOSGroupSessionPicker(model: model, title: "Hand off to", hostID: hostID, writableOnly: true, selection: $to)
            Toggle("Run this handoff once", isOn: $once)
            Button("Add Handoff") {
                Task {
                    if await run({ try await model.groupAction(on: hostID, "relay-add", fromID: from, toID: to, pattern: pattern, once: once, handoff: handoff) }) {
                        (from, to, pattern, once, handoff) = ("", "", ".*", false, "turn_finished")
                    }
                }
            }
            .disabled(busy || from.isEmpty || to.isEmpty)
        } header: {
            Text("New handoff")
        } footer: {
            Text("When one session finishes with matching text, Agenthail sends its reply to another.")
        }
    }
}
