import SwiftUI

struct SigninSettings: View {
    @ObservedObject var model: AgenthailModel
    @State private var browser = ""
    @State private var profile = ""
    @State private var label = ""

    var body: some View {
        Form {
            Section {
                Text("Notion and Claude sessions reached over Remote Control read your sign-in cookies from your system default browser's last used profile. Pick another profile or turn sign-in off for each below, or run agenthail signin use. Nothing else reads cookies.")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
            }
            if let signin = model.signin {
                Section("Used by") {
                    ForEach(signin.consumers) { consumer in
                        VStack(alignment: .leading, spacing: 4) {
                            Picker(consumer.label, selection: Binding(get: { consumer.choice }, set: { model.bindSignin(consumer: consumer.id, choice: $0) })) {
                                Text("Automatic (system default browser)").tag("auto")
                                ForEach(signin.profiles) { Text($0.label).tag($0.id) }
                                Text("Off").tag("none")
                            }
                            Text(consumer.resolvedLabel)
                                .font(.system(size: 12))
                            if let problem = consumer.problem {
                                Text(problem)
                                    .font(.system(size: 12))
                                    .foregroundStyle(DesktopPalette.red)
                                    .textSelection(.enabled)
                            }
                            Text(consumer.purpose)
                                .font(.system(size: 12))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                    }
                }
                Section("Pinned profiles") {
                    if signin.profiles.isEmpty {
                        Text("No pinned profiles. Add one below to use a profile other than the default browser's.")
                            .foregroundStyle(DesktopPalette.text2)
                    }
                    ForEach(signin.profiles) { item in
                        HStack {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(item.label)
                                Text("\(signin.browserLabel(item.browser)) · \(item.profile)")
                                    .font(.system(size: 12))
                                    .foregroundStyle(DesktopPalette.text2)
                            }
                            Spacer()
                            Button("Remove", role: .destructive) { model.removeSigninProfile(item.id) }
                        }
                    }
                }
                Section("Add a browser profile") {
                    let browsers = signin.addableBrowsers
                    if browsers.isEmpty {
                        Text("Every browser profile found on this Mac is already added.")
                            .foregroundStyle(DesktopPalette.text2)
                    } else {
                        Picker("Browser", selection: $browser) {
                            ForEach(browsers) { Text($0.label).tag($0.browser) }
                        }
                        Picker("Profile", selection: $profile) {
                            ForEach(selectedBrowser(signin)?.addableProfiles ?? []) { Text($0.choiceLabel).tag($0.profile) }
                        }
                        TextField("Label (optional)", text: $label)
                        Button("Add Profile") {
                            model.addSigninProfile(browser: browser, profile: profile, label: label)
                            label = ""
                        }
                        .disabled(selectedBrowser(signin)?.addableProfiles.contains { $0.profile == profile } != true)
                    }
                    ForEach(signin.unavailableBrowsers) { item in
                        Text("\(item.label) is unavailable: \(item.detail ?? "no profiles found").")
                            .font(.system(size: 12))
                            .foregroundStyle(DesktopPalette.amber)
                    }
                }
            } else if model.signinError == nil {
                ProgressView().controlSize(.small)
            }
            if let error = model.signinError {
                Label(error, systemImage: "exclamationmark.triangle")
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.red)
                    .textSelection(.enabled)
            }
        }
        .formStyle(.grouped)
        .task { await model.loadSignin() }
        .onChange(of: model.signin) { _, signin in selectDefaults(signin) }
        .onChange(of: browser) { _, _ in
            if let signin = model.signin, selectedBrowser(signin)?.addableProfiles.contains(where: { $0.profile == profile }) != true {
                profile = selectedBrowser(signin)?.addableProfiles.first?.profile ?? ""
            }
        }
    }

    private func selectedBrowser(_ signin: SigninState) -> SigninBrowserState? {
        signin.addableBrowsers.first { $0.browser == browser }
    }

    private func selectDefaults(_ signin: SigninState?) {
        guard let signin else { return }
        if selectedBrowser(signin) == nil { browser = signin.addableBrowsers.first?.browser ?? "" }
        if selectedBrowser(signin)?.addableProfiles.contains(where: { $0.profile == profile }) != true {
            profile = selectedBrowser(signin)?.addableProfiles.first?.profile ?? ""
        }
    }
}
