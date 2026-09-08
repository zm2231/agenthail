import SwiftUI

struct NewSessionSheet: View {
    @ObservedObject var model: AgenthailIOSModel
    @Environment(\.dismiss) private var dismiss
    @State private var options: SessionCreationOptions?
    @State private var selectedSurface = ""
    @State private var cwd = ""
    @State private var selectedModel = ""
    @State private var models: [ModelOption] = []
    @State private var message = ""
    @State private var error: String?
    @State private var modelError: String?
    @State private var loading = false

    var body: some View {
        NavigationStack {
            Form {
                if loading { ProgressView("Loading session options") }
                if let error { Section { Text(error).foregroundStyle(.red); Button("Reload options") { Task { await load() } } } }
                if let options {
                    if options.surfaces.isEmpty {
                        ContentUnavailableView("No agent can start sessions", systemImage: "bubble.left", description: Text("Configure a supported runtime on your Mac, then reload."))
                    } else {
                        Section("Agent") {
                            Picker("Run with", selection: $selectedSurface) {
                                ForEach(options.surfaces) { Text($0.id.capitalized).tag($0.id) }
                            }
                            Text("Uses the runtime configured on your Mac. Claude sessions must currently be started on the Mac.").font(.footnote).foregroundStyle(.secondary)
                        }
                        if options.surfaces.first(where: { $0.id == selectedSurface })?.workspace == true {
                            Section("Workspace on your Mac") {
                                TextField("~/projects/my-project", text: $cwd).textInputAutocapitalization(.never).autocorrectionDisabled()
                                if !options.workspaces.isEmpty {
                                    Menu("Choose a recent workspace") { ForEach(options.workspaces, id: \.self) { path in Button(path) { cwd = path } } }
                                }
                                Text("Leave blank to use your Mac’s home folder. The directory must already exist.").font(.footnote).foregroundStyle(.secondary)
                            }
                        }
                        Section("Model") {
                            Picker("Model", selection: $selectedModel) {
                                Text("Runtime default").tag("")
                                ForEach(models) { Text($0.displayName).tag($0.id) }
                            }
                            if let modelError { Text(modelError).font(.footnote).foregroundStyle(.secondary) }
                            Text("Uses the runtime’s existing permission settings. Requests requiring approval may need attention on your Mac.").font(.footnote).foregroundStyle(.secondary)
                        }
                        Section("First instruction") {
                            TextField("What would you like the agent to do?", text: $message, axis: .vertical).lineLimit(4...12)
                        }
                    }
                }
                if let error = model.creationError { Section { Text(error).foregroundStyle(.red) } }
            }
            .disabled(model.creatingSession)
            .navigationTitle("New conversation")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() }.disabled(model.creatingSession) }
                ToolbarItem(placement: .confirmationAction) {
                    Button { Task {
                        if await model.createSession(surface: selectedSurface, message: message, cwd: cwd, model: selectedModel) { dismiss() }
                    } } label: {
                        if model.creatingSession { ProgressView() } else { Text("Start") }
                    }.disabled(model.creatingSession || selectedSurface.isEmpty || message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .interactiveDismissDisabled(model.creatingSession)
            .task { await load() }
            .task(id: selectedSurface) {
                models = []; selectedModel = ""; modelError = nil
                guard !selectedSurface.isEmpty else { return }
                do { let values = try await model.creationModels(surface: selectedSurface); try Task.checkCancellation(); models = values }
                catch is CancellationError {} catch { modelError = "Models unavailable. You can still use the runtime default." }
            }
        }
    }
    private func load() async {
        loading = true; error = nil
        defer { loading = false }
        do { options = try await model.sessionOptions(); selectedSurface = options?.surfaces.first?.id ?? "" }
        catch { self.error = error.localizedDescription }
    }
}
