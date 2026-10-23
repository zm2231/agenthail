import SwiftUI

struct ProjectMenuButtons: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let project: ProjectState

    var body: some View {
        ForEach(ProjectMenu.items(for: project), id: \.self) { item in
            switch item {
            case .rename:
                Button("Rename…") { pane.projectSheet = .rename(project) }
            case .newConversation(let index, let label):
                Button(label) {
                    let checkout = project.checkouts[index]
                    pane.openNewSession(host: ProjectMenu.host(of: checkout), folder: checkout.rootPath)
                }
            case .lead(let change):
                Button(change ? "Change Lead…" : "Set Lead…") { pane.projectSheet = .lead(project) }
            case .clearLead:
                Button("Clear Lead") {
                    pane.projectConfirmation = ProjectConfirmation(message: "Clear the lead of \(project.displayName)? Its conversation and name stay.", action: "Clear Lead") {
                        await model.clearLead(of: project)
                    }
                }
            case .remove(let projectID, let label):
                Button(label) { pane.changeProject { _ in await model.removeProject(projectID) } }
            }
        }
    }
}

struct ProjectSheetView: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let sheet: ProjectSheet

    var body: some View {
        switch sheet {
        case .add(let host, let folder):
            AddProjectForm(model: model, host: host ?? "", folder: folder)
        case .rename(let project):
            RenameProjectForm(model: model, project: project)
        case .lead(let project):
            ProjectLeadForm(model: model, project: project)
        }
    }
}

private struct ProjectFormFrame<Fields: View>: View {
    let title: String
    let note: String?
    let error: String?
    let saveTitle: String
    let saving: Bool
    let canSave: Bool
    let save: () -> Void
    @ViewBuilder let fields: Fields
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text(title).font(.headline)
            if let note {
                Text(note)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
            }
            fields
            if let error {
                Text(error)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.failed)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(saving ? "Saving…" : saveTitle, action: save)
                    .keyboardShortcut(.defaultAction)
                    .disabled(saving || !canSave)
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 440)
    }
}

private struct AddProjectForm: View {
    @ObservedObject var model: AgenthailModel
    @State var host: String
    @State var folder: String
    @State private var title = ""
    @State private var suggestions: [String] = []
    @State private var saving = false
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss

    private var hosts: [CreationHost] { CreationHostPicker(daemon: model.snapshot?.daemon, selectedSession: nil).hosts }
    private var path: String { folder.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        ProjectFormFrame(title: "Add Project", note: nil, error: error, saveTitle: "Add", saving: saving, canSave: !path.isEmpty, save: save) {
            if hosts.count > 1 {
                Picker("Computer", selection: $host) {
                    ForEach(hosts) { host in
                        Text(host.isLocal ? "\(host.name) · this Mac" : host.name).tag(host.id).disabled(!host.isAvailable)
                    }
                }
            }
            HStack(spacing: DesktopSpace.xs) {
                TextField("Folder", text: $folder)
                    .textFieldStyle(.roundedBorder)
                if !suggestions.isEmpty {
                    Menu("Recent") {
                        ForEach(suggestions, id: \.self) { suggestion in Button(suggestion) { folder = suggestion } }
                    }
                    .fixedSize()
                }
            }
            TextField("Name (optional)", text: $title)
                .textFieldStyle(.roundedBorder)
                .onSubmit(save)
        }
        .task(id: host) {
            suggestions = ProjectMenu.folderSuggestions(try? await model.creationOptions(host: host.isEmpty ? nil : host))
        }
    }

    private func save() {
        guard !path.isEmpty, !saving else { return }
        saving = true
        Task {
            let change = await model.addProject(path: path, title: title.trimmingCharacters(in: .whitespacesAndNewlines), host: host.isEmpty ? nil : host)
            saving = false
            if case .failed(let message) = change { error = message } else { dismiss() }
        }
    }
}

private struct RenameProjectForm: View {
    @ObservedObject var model: AgenthailModel
    let project: ProjectState
    @State private var title: String
    @State private var saving = false
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss

    init(model: AgenthailModel, project: ProjectState) {
        self.model = model
        self.project = project
        _title = State(initialValue: project.displayName)
    }

    private var trimmed: String { title.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        ProjectFormFrame(title: "Rename \(project.displayName)", note: "The name applies on every computer where this project was added.", error: error, saveTitle: "Save", saving: saving, canSave: !trimmed.isEmpty && trimmed.count <= 120 && trimmed != project.displayName, save: save) {
            TextField("Name", text: $title)
                .textFieldStyle(.roundedBorder)
                .onSubmit(save)
        }
    }

    private func save() {
        guard !trimmed.isEmpty, !saving else { return }
        saving = true
        Task {
            let change = await model.renameProject(project.id, to: trimmed)
            saving = false
            if case .failed(let message) = change { error = message } else { dismiss() }
        }
    }
}

private struct ProjectLeadForm: View {
    @ObservedObject var model: AgenthailModel
    let project: ProjectState
    @State private var checkout = 0
    @State private var surface = "claude"
    @State private var name = ""
    @State private var saving = false
    @State private var error: String?
    @State private var replacing: String?
    @Environment(\.dismiss) private var dismiss

    private var usable: [(index: Int, checkout: ProjectState.Checkout)] {
        project.checkouts.enumerated().filter { $0.element.archived != true && ["local", "connected"].contains($0.element.host.state) }.map { ($0.offset, $0.element) }
    }

    private var note: String {
        guard !project.leads.isEmpty else { return "Starts a lead conversation. Its first message starts it." }
        return "Starts a new lead in place of \(project.leads.map { "@\($0.name)" }.joined(separator: " and ")). To make an existing conversation the lead, use Make Project Lead on it."
    }

    var body: some View {
        ProjectFormFrame(title: "Lead of \(project.displayName)", note: note, error: error ?? (usable.isEmpty ? "No computer with this project is connected." : nil), saveTitle: "Start Lead", saving: saving, canSave: !usable.isEmpty, save: { start(replace: false) }) {
            if usable.count > 1 {
                Picker("Checkout", selection: $checkout) {
                    ForEach(usable, id: \.index) { entry in Text("\(entry.checkout.host.name): \(entry.checkout.rootPath)").tag(entry.index) }
                }
            }
            Picker("Agent", selection: $surface) {
                Text("Claude").tag("claude")
                Text("Codex").tag("codex")
            }
            .pickerStyle(.segmented)
            TextField("Name (optional)", text: $name)
                .textFieldStyle(.roundedBorder)
        }
        .onAppear { checkout = usable.first?.index ?? 0 }
        .alert(replacing ?? "", isPresented: Binding(get: { replacing != nil }, set: { if !$0 { replacing = nil } })) {
            Button("Replace", role: .destructive) { start(replace: true) }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func start(replace: Bool) {
        guard let entry = usable.first(where: { $0.index == checkout }) ?? usable.first, !saving else { return }
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
