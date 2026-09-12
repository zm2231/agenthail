import SwiftUI

struct SearchableModelSelectionSheet: View {
    let initialOptions: [ModelOption]
    let currentSelectedID: String?
    let allowsDefault: Bool
    let onSelect: (String?) -> Void
    let reload: () async throws -> [ModelOption]

    @Environment(\.dismiss) private var dismiss
    @State private var options: [ModelOption]
    @State private var pendingSelection: String?
    @State private var customID: String
    @State private var query = ""
    @State private var searching = false
    @FocusState private var customFocused: Bool
    @State private var isReloading = false
    @State private var hasLoaded = false
    @State private var reloadError: String?

    init(initialOptions: [ModelOption], currentSelectedID: String?, allowsDefault: Bool, onSelect: @escaping (String?) -> Void, reload: @escaping () async throws -> [ModelOption]) {
        self.initialOptions = initialOptions
        self.currentSelectedID = currentSelectedID
        self.allowsDefault = allowsDefault
        self.onSelect = onSelect
        self.reload = reload
        _options = State(initialValue: initialOptions)
        _pendingSelection = State(initialValue: currentSelectedID)
        _customID = State(initialValue: Self.customValue(currentSelectedID, options: initialOptions))
    }

    private var filteredOptions: [ModelOption] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !needle.isEmpty else { return options }
        return options.filter { option in
            option.id.localizedCaseInsensitiveContains(needle) || option.displayName.localizedCaseInsensitiveContains(needle) || (option.description?.localizedCaseInsensitiveContains(needle) ?? false)
        }
    }

    private var supportsCustomID: Bool { options.contains { $0.allowsCustom == true } }

    private var canApply: Bool {
        allowsDefault || !(pendingSelection?.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ?? true)
    }

    private var selectedIDMissingFromCatalog: String? {
        guard let currentSelectedID, !currentSelectedID.isEmpty, !options.contains(where: { $0.id == currentSelectedID }) else { return nil }
        return currentSelectedID
    }

    var body: some View {
        NavigationStack {
            List {
                if allowsDefault { selectionRow(id: nil, title: "Runtime default", detail: nil) }
                if let selectedIDMissingFromCatalog {
                    Section("Current selection") {
                        Label(selectedIDMissingFromCatalog, systemImage: "questionmark.circle")
                        Text("The runtime did not return this ID in the latest catalog.").font(.footnote).foregroundStyle(.secondary)
                    }
                }
                if isReloading && !hasLoaded {
                    ProgressView("Loading model options")
                } else {
                    if let reloadError {
                        Section {
                            Label(reloadError, systemImage: "exclamationmark.triangle").foregroundStyle(.secondary)
                        }
                    }
                    if filteredOptions.isEmpty && reloadError == nil {
                        ContentUnavailableView("No model options", systemImage: "rectangle.3.group", description: Text(query.isEmpty ? "The runtime returned an empty catalog." : "No model matches \"\(query)\"."))
                    } else {
                        ForEach(filteredOptions) { option in
                            selectionRow(id: option.id, title: option.displayName, detail: option.description ?? option.id)
                        }
                    }
                }
                if supportsCustomID {
                    Section("Custom model ID") {
                        TextField("Enter an explicit model ID", text: $customID)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .focused($customFocused)
                        Button("Use custom model ID") {
                            let value = customID.trimmingCharacters(in: .whitespacesAndNewlines)
                            guard !value.isEmpty else { return }
                            pendingSelection = value
                            customFocused = false
                        }
                        .disabled(customID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                        if let pendingSelection, !options.contains(where: { $0.id == pendingSelection }) {
                            Label(pendingSelection, systemImage: "checkmark").font(.footnote)
                        }
                    }
                }
                Section {
                    Button("Refresh model options") { Task { await reloadOptions() } }.disabled(isReloading)
                }
            }
            .searchable(text: $query, isPresented: $searching, prompt: "Search model options")
            .navigationTitle("Model")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Apply") { onSelect(pendingSelection); dismiss() }.disabled(!canApply)
                }
            }
            .task { await reloadOptions() }
        }
    }

    @ViewBuilder
    private func selectionRow(id: String?, title: String, detail: String?) -> some View {
        Button {
            pendingSelection = id
            searching = false
            if let id, !options.contains(where: { $0.id == id }) { customID = id }
        } label: {
            HStack {
                VStack(alignment: .leading) {
                    Text(title)
                    if let detail { Text(detail).font(.footnote).foregroundStyle(.secondary) }
                }
                Spacer()
                if pendingSelection == id { Image(systemName: "checkmark") }
            }
        }.buttonStyle(.plain)
    }

    private func reloadOptions() async {
        isReloading = true
        reloadError = nil
        defer { isReloading = false }
        do {
            options = try await reload()
            hasLoaded = true
            if let pendingSelection, !options.contains(where: { $0.id == pendingSelection }) { customID = pendingSelection }
        } catch {
            hasLoaded = true
            reloadError = error.localizedDescription
        }
    }

    private static func customValue(_ selection: String?, options: [ModelOption]) -> String {
        guard let selection, !selection.isEmpty, !options.contains(where: { $0.id == selection }) else { return "" }
        return selection
    }
}
