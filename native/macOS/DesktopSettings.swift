import SwiftUI

struct DesktopSettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        TabView {
            GeneralSettings()
                .tabItem { Label("General", systemImage: "gearshape") }
            OperationsView(model: model)
                .tabItem { Label("Phone & relays", systemImage: "iphone") }
        }
        .frame(width: 760, height: 560)
    }
}

struct GeneralSettings: View {
    @AppStorage("followUpDefault") private var followUpDefault = FollowUpAction.queue.rawValue

    var body: some View {
        Form {
            Picker("While an agent is working, ⌘↩", selection: $followUpDefault) {
                Text("Queue the message (default)").tag(FollowUpAction.queue.rawValue)
                Text("Steer the running turn").tag(FollowUpAction.steer.rawValue)
            }
            .pickerStyle(.radioGroup)
            Text("⌥⌘↩ always does the other one. Agents that can't be steered mid-turn send queued messages after the turn.")
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
        }
        .formStyle(.grouped)
    }
}
