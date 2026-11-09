import SwiftUI

struct HostGroupsScreen: View {
    var model: AgenthailIOSModel
    let hostID: String
    let hostName: String
    @State private var problem: String?
    @State private var busy = false

    var body: some View {
        let groups = model.hostGroups[hostID] ?? HostGroups()
        List {
            if let problem {
                Text(problem).foregroundStyle(PhonePalette.failed).accessibilityIdentifier("groups-problem")
            }
            IOSChannelsSection(model: model, hostID: hostID, channels: groups.channels, busy: busy, run: run)
            IOSHandoffsSection(model: model, hostID: hostID, relays: groups.relays, busy: busy, run: run)
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Channels & handoffs")
        .navigationBarTitleDisplayMode(.inline)
        .refreshable { await model.refresh(fresh: true) }
    }

    private func run(_ work: @escaping @MainActor () async throws -> Void) async -> Bool {
        guard !busy else { return false }
        busy = true
        defer { busy = false }
        problem = nil
        do {
            try await work()
            return true
        } catch {
            problem = AgenthailAPIError.userMessage(error)
            return false
        }
    }
}

struct IOSGroupSessionPicker: View {
    var model: AgenthailIOSModel
    let title: String
    let hostID: String
    var writableOnly = false
    @Binding var selection: String

    var body: some View {
        Picker(title, selection: $selection) {
            Text("Choose a session").tag("")
            ForEach(model.groupSessions(on: hostID, writableOnly: writableOnly)) { Text($0.title).tag($0.id) }
        }
    }
}
