import SwiftUI

struct NewSessionPlace: Identifiable {
    let host: String
    let folder: String
    var id: String { host + "\u{0}" + folder }
}

struct IOSProjectMenuItems: View {
    @ObservedObject var model: AgenthailIOSModel
    let project: ListedProject
    @Binding var sheet: ProjectSheet?
    @Binding var confirmation: ProjectConfirmation?
    @Binding var place: NewSessionPlace?

    var body: some View {
        ForEach(ProjectMenu.items(for: project.state), id: \.self) { item in
            switch item {
            case .rename:
                Button("Rename…", systemImage: "pencil") { sheet = .rename(project.state) }
            case .newConversation(let index, let label):
                Button(label, systemImage: "square.and.pencil") {
                    let checkout = project.state.checkouts[index]
                    place = NewSessionPlace(host: model.creationHost(for: checkout, of: project), folder: checkout.rootPath)
                }
            case .lead(let change):
                Button(change ? "Change lead…" : "Set lead…", systemImage: "person.badge.key") { sheet = .lead(project.state) }
            case .clearLead:
                Button("Clear lead", systemImage: "person.badge.minus") {
                    confirmation = ProjectConfirmation(message: "Clear the lead of \(project.state.displayName)? Its conversation and name stay.", action: "Clear lead") {
                        await model.clearLead(of: project)
                    }
                }
            case .remove(let projectID, let label):
                Button(label, systemImage: "minus.circle", role: .destructive) {
                    Task { model.settle(await model.removeProject(projectID, of: project)) }
                }
            }
        }
    }
}

struct IOSProjectSheetView: View {
    @ObservedObject var model: AgenthailIOSModel
    let sheet: ProjectSheet

    var body: some View {
        NavigationStack {
            switch sheet {
            case .add(let host, let folder):
                IOSAddProjectForm(model: model, host: host ?? model.creationHosts.first { $0.isAvailable }?.id ?? "", folder: folder)
            case .rename(let state):
                if let project = model.listed(state) { IOSRenameProjectForm(model: model, project: project) }
            case .lead(let state):
                if let project = model.listed(state) { IOSProjectLeadForm(model: model, project: project) }
            }
        }
    }
}

private struct IOSAddProjectForm: View {
    @ObservedObject var model: AgenthailIOSModel
    @State var host: String
    @State var folder: String
    @State private var title = ""
    @State private var suggestions: [String] = []
    @State private var saving = false
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss

    private var path: String { folder.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        Form {
            if model.creationHosts.count > 1 {
                Picker("Computer", selection: $host) {
                    ForEach(model.creationHosts) { host in Text(host.name).tag(host.id).disabled(!host.isAvailable) }
                }
            }
            Section("Folder") {
                TextField("/path/to/folder", text: $folder).textInputAutocapitalization(.never).autocorrectionDisabled()
                ForEach(suggestions, id: \.self) { suggestion in
                    Button(suggestion) { folder = suggestion }.font(.footnote)
                }
            }
            Section("Name") {
                TextField("Optional", text: $title)
            }
            if let error { Text(error).font(.footnote).foregroundStyle(PhonePalette.failed) }
        }
        .navigationTitle("Add project").navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) { Button(saving ? "Adding…" : "Add", action: save).disabled(path.isEmpty || saving) }
        }
        .task(id: host) {
            suggestions = ProjectMenu.folderSuggestions(try? await model.sessionOptions(host: host.isEmpty ? nil : host))
        }
    }

    private func save() {
        saving = true
        Task {
            let change = await model.addProject(path: path, title: title.trimmingCharacters(in: .whitespacesAndNewlines), host: host.isEmpty ? nil : host)
            saving = false
            if case .failed(let message) = change { error = message } else { dismiss() }
        }
    }
}

private struct IOSRenameProjectForm: View {
    @ObservedObject var model: AgenthailIOSModel
    let project: ListedProject
    @State private var title: String
    @State private var saving = false
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss

    init(model: AgenthailIOSModel, project: ListedProject) {
        self.model = model
        self.project = project
        _title = State(initialValue: project.state.displayName)
    }

    private var trimmed: String { title.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        Form {
            Section {
                TextField("Name", text: $title)
            } footer: {
                Text("The name applies on every computer where this project was added.")
            }
            if let error { Text(error).font(.footnote).foregroundStyle(PhonePalette.failed) }
        }
        .navigationTitle("Rename \(project.state.displayName)").navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button(saving ? "Saving…" : "Save", action: save).disabled(trimmed.isEmpty || trimmed.count > 120 || trimmed == project.state.displayName || saving)
            }
        }
    }

    private func save() {
        saving = true
        Task {
            let change = await model.renameProject(project, to: trimmed)
            saving = false
            if case .failed(let message) = change { error = message } else { dismiss() }
        }
    }
}

private struct IOSProjectLeadForm: View {
    @ObservedObject var model: AgenthailIOSModel
    let project: ListedProject
    @State private var checkout = 0
    @State private var surface = "claude"
    @State private var name = ""
    @State private var saving = false
    @State private var error: String?
    @State private var replacing: String?
    @Environment(\.dismiss) private var dismiss

    private var usable: [(index: Int, checkout: ProjectState.Checkout)] {
        project.state.checkouts.enumerated().filter { $0.element.archived != true && ["local", "connected"].contains($0.element.host.state) }.map { ($0.offset, $0.element) }
    }

    var body: some View {
        Form {
            Section {
                if usable.count > 1 {
                    Picker("Computer", selection: $checkout) {
                        ForEach(usable, id: \.index) { entry in Text(entry.checkout.host.name).tag(entry.index) }
                    }
                }
                Picker("Agent", selection: $surface) {
                    Text("Claude").tag("claude")
                    Text("Codex").tag("codex")
                }
                TextField("Name (optional)", text: $name).textInputAutocapitalization(.never).autocorrectionDisabled()
            } footer: {
                Text(project.state.leads.isEmpty ? "Starts a lead conversation. Its first message starts it." : "Starts a new lead in place of \(project.state.leads.map { "@\($0.name)" }.joined(separator: " and ")). To make an existing conversation the lead, use Make project lead in its details.")
            }
            if let message = error ?? (usable.isEmpty ? "No computer with this project is connected." : nil) {
                Text(message).font(.footnote).foregroundStyle(PhonePalette.failed)
            }
        }
        .navigationTitle("Lead of \(project.state.displayName)").navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) { Button(saving ? "Starting…" : "Start", action: { start(replace: false) }).disabled(usable.isEmpty || saving) }
        }
        .onAppear { checkout = usable.first?.index ?? 0 }
        .alert(replacing ?? "", isPresented: Binding(get: { replacing != nil }, set: { if !$0 { replacing = nil } })) {
            Button("Replace", role: .destructive) { start(replace: true) }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func start(replace: Bool) {
        guard let entry = usable.first(where: { $0.index == checkout }) ?? usable.first else { return }
        saving = true
        Task {
            let change = await model.startLead(of: project, in: entry.checkout, surface: surface, name: name.trimmingCharacters(in: .whitespacesAndNewlines), replace: replace)
            saving = false
            switch change {
            case .done: dismiss()
            case .confirmReplace(let message): replacing = message
            case .failed(let message): error = message
            }
        }
    }
}

struct IOSSessionProjectControls: View {
    @ObservedObject var model: AgenthailIOSModel
    let session: SessionState
    @State private var replacing: String?

    var body: some View {
        if model.canAddFolder(session) {
            Button("Add folder as project", systemImage: "folder.badge.plus") {
                Task { model.settle(await model.addFolder(of: session)) }
            }
        }
        if model.canMakeLead(session) {
            Button("Make project lead", systemImage: "person.badge.key") { makeLead(replace: false) }
                .alert(replacing ?? "", isPresented: Binding(get: { replacing != nil }, set: { if !$0 { replacing = nil } })) {
                    Button("Replace", role: .destructive) { makeLead(replace: true) }
                    Button("Cancel", role: .cancel) {}
                }
        }
    }

    private func makeLead(replace: Bool) {
        Task {
            let change = await model.makeLead(session.id, replace: replace)
            if case .confirmReplace(let message) = change { replacing = message } else { model.settle(change) }
        }
    }
}
