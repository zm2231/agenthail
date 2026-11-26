import SwiftUI

struct AgentSwitchSettings: View {
    var model: AgenthailModel

    var body: some View {
        Form {
            Section {
                Text("Agenthail works with each agent installed on this Mac. One that is not installed is unavailable, not a problem. Turn an agent on to make a missing one a failure, or off to leave it alone. agenthail agents use sets the same thing.")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            }
            Section("Agents") {
                ForEach(model.settings?.agents ?? []) { agent in
                    VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                        Picker(agent.label, selection: Binding(get: { agent.mode }, set: { model.setAgentMode(agent.id, mode: $0) })) {
                            Text("Automatic (when installed)").tag("auto")
                            Text("On").tag("on")
                            Text("Off").tag("off")
                        }
                        Text(AgentSwitchSettings.stateLine(agent))
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(agent.state == "missing" ? DesktopPalette.failed : DesktopPalette.text2)
                            .textSelection(.enabled)
                    }
                }
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
    }

    static func stateLine(_ agent: AgentSwitchState) -> String {
        switch agent.state {
        case "on": return "In use on this Mac."
        case "off": return "Off: Agenthail leaves \(agent.label) alone."
        case "unavailable": return "Not installed on this Mac."
        default: return agent.detail ?? "\(agent.label) is on but not installed."
        }
    }
}
