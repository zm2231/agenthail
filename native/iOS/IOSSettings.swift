import SwiftUI

struct SettingsView: View {
    var model: AgenthailIOSModel
    var body: some View {
        List {
            Group {
                Section {
                    NavigationLink("Add a computer") { PairingForm(model: model, adding: true) }
                }
                ForEach(model.hosts) { host in
                    Section {
                        Label(connectionLabel(host), systemImage: connectionIcon(host))
                        if let error = host.error { Text(error).font(.footnote).foregroundStyle(PhonePalette.muted) }
                        if let build = host.build {
                            LabeledContent("Agenthail", value: build.label).font(.footnote).textSelection(.enabled)
                        }
                        ForEach(host.surfaces) { surface in
                            VStack(alignment: .leading, spacing: 4) {
                                LabeledContent(surface.name.capitalized, value: surface.connected ? "Needs attention" : "Unavailable")
                                DisclosureGroup("Details") {
                                    if surface.runtime?.advice.isEmpty ?? true, let detail = surface.healthDetail {
                                        Text(detail).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                    }
                                    ForEach(surface.runtime?.advice ?? [], id: \.self) { line in
                                        Text(line).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                    }
                                }
                                .font(.footnote)
                            }
                        }
                        ForEach(host.signin) { consumer in
                            VStack(alignment: .leading, spacing: 4) {
                                Text("Browser sign-in: \(consumer.label)").font(.headline)
                                Text(consumer.resolvedLabel).font(.footnote)
                                if let problem = consumer.problem {
                                    Text(problem).font(.footnote).foregroundStyle(PhonePalette.attention).textSelection(.enabled)
                                }
                                if let unavailable = consumer.unavailable {
                                    Text(unavailable).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                }
                            }
                        }
                        ForEach(host.peers) { peer in
                            VStack(alignment: .leading, spacing: 4) {
                                LabeledContent(peer.name, value: peer.stateLabel)
                                if let build = peer.build {
                                    Text(build.label).font(.footnote).foregroundStyle(PhonePalette.muted).textSelection(.enabled)
                                }
                                if let detail = peer.detail, !detail.isEmpty {
                                    Text(detail).font(.footnote).foregroundStyle(PhonePalette.muted)
                                }
                                if let notice = peer.notice, !notice.isEmpty {
                                    Text(notice).font(.footnote).foregroundStyle(RoleStyle(.body)).lineLimit(3)
                                }
                            }
                        }
                        if let person = host.person {
                            VStack(alignment: .leading, spacing: 4) {
                                LabeledContent("Your name", value: person.name)
                                Text(person.set ? "Set in Settings on \(host.name) or with agenthail person name." : "\(host.name)'s account name. Set yours in Settings on \(host.name) or with agenthail person name.")
                                    .font(.footnote).foregroundStyle(PhonePalette.muted)
                            }
                        }
                        if model.hosts.count > 1 { LabeledContent("Notifications", value: host.notifications) }
                        NavigationLink("Channels & handoffs") { HostGroupsScreen(model: model, hostID: host.id, hostName: host.name) }
                            .accessibilityIdentifier("host-groups-" + host.id)
                        Button("Reconnect") { model.reconnect(host: host.id) }
                        Button("Remove \(host.name)", role: .destructive) { model.removeHost(host.id) }
                    } header: {
                        Text(host.name)
                    } footer: {
                        if !host.signin.isEmpty {
                            Text("Change browser sign-in in Settings > Browser Sign-in on \(host.name), or run agenthail signin use there.")
                        } else {
                            Text(host.address)
                        }
                    }
                }
                if model.hosts.count > 1 {
                    Section {
                        Picker("Orchestrator", selection: Binding(get: { model.voiceHost?.id ?? "" }, set: { model.voiceHostID = $0 })) {
                            ForEach(model.hosts) { Text($0.name).tag($0.id) }
                        }
                    } header: {
                        Text("Voice")
                    } footer: {
                        Text("Talk to orchestrator calls this computer.")
                    }
                }
                AgentsSettingsSection(model: model)
                TurnNoticeSettingsSection()
                PhoneTextSizeSection(model: model)
                Section("Notifications") {
                    LabeledContent("Status", value: model.notificationStatus)
                    if model.notificationStatus == "Enabling" {
                        Label { Text("Setting up notifications…") } icon: { ProgressView() }
                            .accessibilityIdentifier("notifications-setting-up")
                    } else if model.notificationStatus == "Enabled" {
                        Button("Turn off notifications", role: .destructive) { model.turnOffNotifications() }
                    } else if model.notificationStatus == "Not allowed" {
                        Button("Open iOS Settings") { model.openNotificationSettings() }
                    } else if model.notificationStatus == "Setup failed" {
                        Button("Retry notification setup") { model.requestNotifications() }
                    } else {
                        Button("Enable notifications") { model.requestNotifications() }
                    }
                    ForEach(model.notificationProblems, id: \.self) { problem in
                        Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed)
                            .accessibilityIdentifier("notifications-problem")
                    }
                    Text("Your computers send an alert when an agent finishes or fails.").font(.footnote).foregroundStyle(PhonePalette.muted)
                    ForEach(model.notificationPreferences) { preference in
                        Toggle(isOn: Binding(get: { preference.value }, set: { model.setNotificationPreference(preference.id, to: $0) })) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(preference.title)
                                Text(preference.detail).font(.footnote).foregroundStyle(PhonePalette.muted)
                            }
                        }
                        .accessibilityIdentifier("notification-preference-\(preference.id)")
                    }
                    if let problem = model.notificationPreferenceProblem {
                        Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed)
                    }
                }
                .task { await model.loadNotificationPreferences() }
            }
            .listRowBackground(GlassRowBackground(selected: false))
        }
        .phoneBackdrop(.pane)
        .navigationTitle("Settings")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func connectionLabel(_ host: PairedHostStatus) -> String {
        if host.error != nil { return "Connection interrupted" }
        if host.reconnecting { return "Reconnecting live updates" }
        return host.reachable ? "Connected over Tailscale" : "Not connected"
    }

    private func connectionIcon(_ host: PairedHostStatus) -> String {
        if host.error != nil { return "wifi.exclamationmark" }
        if host.reconnecting { return "arrow.triangle.2.circlepath" }
        return host.reachable ? "lock.shield.fill" : "wifi.slash"
    }
}
