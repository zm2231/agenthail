import SwiftUI
import UIKit
import WebKit

struct ExtensionsHub: View {
    var model: AgenthailIOSModel
    @State private var showsHistory: CatalogBox?
    @State private var showsNew: CatalogBox?
    private var pins: ExtensionPins { .shared }

    var body: some View {
        List {
            ForEach(model.extensionHosts, id: \.hostID) { catalog in
                Section {
                    if let error = catalog.error {
                        Text(error).font(.footnote).foregroundStyle(PhonePalette.muted)
                    }
                    if catalog.entries.isEmpty && catalog.error == nil {
                        Text(catalog.list == nil ? "Looking for extensions…" : "No extensions yet.")
                            .foregroundStyle(PhonePalette.muted)
                    }
                    ForEach(catalog.entries) { entry in
                        row(entry)
                    }
                } header: {
                    HStack {
                        Text(catalog.hostName)
                        Spacer()
                        if catalog.api != nil {
                            Menu {
                                Button("History", systemImage: "clock.arrow.circlepath") { showsHistory = CatalogBox(catalog: catalog) }
                                Button("New Extension", systemImage: "plus") { showsNew = CatalogBox(catalog: catalog) }
                            } label: {
                                Image(systemName: "ellipsis.circle")
                            }
                            .accessibilityLabel("Actions for \(catalog.hostName)")
                        }
                    }
                }
                .listRowBackground(GlassRowBackground(selected: false))
            }
            Section {
                EmptyView()
            } footer: {
                Text(footer)
            }
        }
        .listStyle(.insetGrouped)
        .phoneBackdrop(.pane)
        .navigationTitle("Apps")
        .navigationDestination(for: ExtensionRoute.self) { route in
            ExtensionPageScreen(model: model, entryID: route.entryID)
        }
        .refreshable { model.extensionHosts.forEach { $0.refresh() } }
        .sheet(item: $showsHistory) { box in
            if let api = box.catalog.api { NavigationStack { ExtensionHistoryList(api: api) } }
        }
        .sheet(item: $showsNew) { box in
            if let api = box.catalog.api {
                NavigationStack {
                    NewExtensionForm(api: api) { sessionID in model.openSession(HostCatalog.qualify(box.catalog.hostID, sessionID)) }
                }
            }
        }
    }

    private var footer: String {
        let others = model.hostsWithoutExtensions
        let base = "Extensions on the computers this phone is paired with."
        guard !others.isEmpty else { return base }
        return base + " To open extensions on \(ListFormatter.localizedString(byJoining: others)), pair this phone with it."
    }

    private func row(_ entry: ExtensionEntry) -> some View {
        let pinned = pins.isPinned(entry)
        return NavigationLink(value: ExtensionRoute(entryID: entry.id)) {
            HStack(spacing: 12) {
                ExtensionSymbol(name: entry.icon)
                    .frame(width: 28)
                VStack(alignment: .leading, spacing: 2) {
                    Text(entry.label)
                    if let detail = entry.unavailable ?? entry.detail {
                        Text(detail)
                            .font(.footnote)
                            .foregroundStyle(PhonePalette.muted)
                            .lineLimit(2)
                    }
                }
                Spacer()
                if let badge = entry.badge {
                    Text(badge).font(.footnote.weight(.semibold)).monospacedDigit().foregroundStyle(.tint)
                }
                if pinned { Image(systemName: "pin.fill").font(.caption).foregroundStyle(PhonePalette.muted) }
            }
        }
        .disabled(entry.url == nil)
        .swipeActions(edge: .trailing) {
            Button(pinned ? "Unpin" : "Pin", systemImage: pinned ? "pin.slash" : "pin") { pins.toggle(entry) }
                .tint(.orange)
        }
        .contextMenu {
            Button(pinned ? "Remove from Tab Bar" : (entry.offered ? "Add to Tab Bar (it asks for a tab)" : "Add to Tab Bar"), systemImage: pinned ? "pin.slash" : "pin") { pins.toggle(entry) }
        }
        .accessibilityIdentifier("extension-entry-" + entry.id)
    }
}

struct ExtensionRoute: Hashable {
    let entryID: String
}

private struct CatalogBox: Identifiable {
    let catalog: ExtensionCatalog
    var id: ObjectIdentifier { ObjectIdentifier(catalog) }
}

struct ExtensionSymbol: View {
    let name: String

    var body: some View {
        Image(systemName: UIImage(systemName: name) == nil ? ExtensionEntry.fallbackIcon : name)
    }
}

struct ExtensionPageScreen: View {
    var model: AgenthailIOSModel
    let entryID: String
    @State private var fullScreen = false

    var body: some View {
        let entry = model.extensionEntry(entryID)
        Group {
            if let entry, let url = entry.url {
                let controller = ExtensionPages.controller(for: entry)
                ExtensionPageView(controller: controller, url: url, signIn: model.extensionCatalogs[entry.hostID]?.signIn)
                    .overlay {
                        if case .failed(let message) = controller.phase {
                            ContentUnavailableView {
                                Label(entry.label, systemImage: "exclamationmark.triangle")
                            } description: {
                                Text(message)
                            } actions: {
                                Button("Reload") { controller.reload() }
                            }
                            .background(.background)
                        }
                    }
                    .ignoresSafeArea(.container, edges: fullScreen ? .all : [.bottom, .horizontal])
                    .overlay(alignment: .topTrailing) {
                        if fullScreen {
                            Button { fullScreen = false } label: {
                                Image(systemName: "arrow.down.right.and.arrow.up.left")
                                    .padding(10)
                                    .background(.regularMaterial, in: Circle())
                            }
                            .padding()
                            .accessibilityLabel("Exit full screen")
                            .accessibilityIdentifier("extension-exit-full-screen")
                        }
                    }
            } else if let entry {
                ContentUnavailableView(entry.label, systemImage: ExtensionEntry.fallbackIcon, description: Text(entry.unavailable ?? "This page can't open right now."))
            } else if model.extensionHosts.contains(where: { $0.list == nil }) {
                ProgressView()
            } else {
                ContentUnavailableView("This extension was removed", systemImage: ExtensionEntry.fallbackIcon, description: Text("It is no longer on any computer this phone is paired with."))
                    .onAppear { ExtensionPages.forget(entryID) }
            }
        }
        .navigationTitle(entry?.label ?? "Extension")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(fullScreen ? .hidden : .automatic, for: .navigationBar, .tabBar)
        .statusBarHidden(fullScreen)
        .persistentSystemOverlays(fullScreen ? .hidden : .automatic)
        .toolbar {
            if let entry, entry.url != nil {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button("Reload", systemImage: "arrow.clockwise") { ExtensionPages.controller(for: entry).reload() }
                        .accessibilityIdentifier("extension-reload")
                    Button("Full Screen", systemImage: "arrow.up.left.and.arrow.down.right") { fullScreen = true }
                        .accessibilityIdentifier("extension-full-screen")
                }
            }
        }
    }
}

private struct ExtensionPageView: UIViewRepresentable {
    let controller: ExtensionPageController
    let url: URL
    let signIn: ExtensionSignIn?

    func makeUIView(context: Context) -> UIView {
        controller.openOutside = { UIApplication.shared.open($0) }
        let host = UIView()
        attach(to: host)
        return host
    }

    func updateUIView(_ host: UIView, context: Context) {
        if controller.webView.superview !== host { attach(to: host) }
        controller.open(url, signIn: signIn)
    }

    private func attach(to host: UIView) {
        let webView = controller.webView
        webView.removeFromSuperview()
        webView.translatesAutoresizingMaskIntoConstraints = false
        host.addSubview(webView)
        NSLayoutConstraint.activate([
            webView.leadingAnchor.constraint(equalTo: host.leadingAnchor),
            webView.trailingAnchor.constraint(equalTo: host.trailingAnchor),
            webView.topAnchor.constraint(equalTo: host.topAnchor),
            webView.bottomAnchor.constraint(equalTo: host.bottomAnchor),
        ])
    }
}

struct ExtensionHistoryList: View {
    let api: AgenthailAPI
    @Environment(\.dismiss) private var dismiss
    @State private var changes: [ExtensionChange]?
    @State private var failure: String?
    @State private var undoing: String?

    var body: some View {
        List {
            Section {
                if let failure { Text(failure).foregroundStyle(PhonePalette.failed) }
                if let changes, changes.isEmpty { Text("No changes recorded yet.").foregroundStyle(PhonePalette.muted) }
                ForEach(changes ?? []) { change in
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(ExtensionHistory.describe(change))
                            Text(change.time.formatted(date: .abbreviated, time: .shortened))
                                .font(.footnote)
                                .foregroundStyle(PhonePalette.muted)
                        }
                        Spacer()
                        if change.root != true {
                            Button("Undo") { Task { await undo(change) } }
                                .buttonStyle(.bordered)
                                .disabled(undoing != nil)
                        }
                    }
                }
            } footer: {
                Text("Changes are kept as they are made. Undo any change here.")
            }
        }
        .overlay { if changes == nil && failure == nil { ProgressView() } }
        .navigationTitle("Extension History")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        .task { await load() }
    }

    private func load() async {
        do { changes = try await api.extensionChanges() } catch { failure = error.localizedDescription }
    }

    private func undo(_ change: ExtensionChange) async {
        undoing = change.rev
        defer { undoing = nil }
        do {
            try await api.undoExtensionChange(change.rev)
            failure = nil
            await load()
        } catch {
            failure = error.localizedDescription
        }
    }
}

struct NewExtensionForm: View {
    let api: AgenthailAPI
    let started: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var message = ""
    @State private var starting = false
    @State private var failure: String?

    var body: some View {
        Form {
            Section {
                TextField("What should it show or do?", text: $message, axis: .vertical)
                    .lineLimit(4...10)
                    .accessibilityIdentifier("new-extension-message")
            } footer: {
                Text("A builder session starts, and its changes are kept as it makes them.")
            }
            if let failure { Text(failure).foregroundStyle(PhonePalette.failed) }
        }
        .navigationTitle("New Extension")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) {
                Button("Start") { Task { await start() } }
                    .disabled(starting || message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    private func start() async {
        starting = true
        defer { starting = false }
        do {
            let created = try await api.buildExtension(message.trimmingCharacters(in: .whitespacesAndNewlines))
            dismiss()
            if let id = created.session?.id { started(id) }
        } catch {
            failure = error.localizedDescription
        }
    }
}
