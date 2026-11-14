import AppKit
import SwiftUI
import WebKit

struct ExtensionsPane: View {
    var model: AgenthailModel
    var pane: SessionPane
    @State private var showsHistory = false
    @State private var showsNew = false
    private var pins: ExtensionPins { .shared }

    var body: some View {
        let catalog = model.extensionCatalog
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.l) {
                HStack(alignment: .firstTextBaseline) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Apps")
                            .font(.system(size: DesktopType.display, weight: .semibold))
                            .foregroundStyle(DesktopPalette.text)
                        Text(catalog.hostName.isEmpty ? "Extensions on this computer" : "Extensions on \(catalog.hostName)")
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                    Spacer()
                    Button("History") { showsHistory = true }
                        .accessibilityIdentifier("extensions-history")
                    Button("New Extension…") { showsNew = true }
                        .accessibilityIdentifier("extensions-new")
                }
                if let error = catalog.error {
                    Text(error)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.text2)
                }
                if catalog.entries.isEmpty {
                    Text(catalog.list == nil ? "Looking for extensions…" : "No extensions yet. Use New Extension to describe one.")
                        .font(.system(size: DesktopType.body))
                        .foregroundStyle(DesktopPalette.text2)
                } else {
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 180, maximum: 260), spacing: DesktopSpace.m, alignment: .top)], alignment: .leading, spacing: DesktopSpace.m) {
                        ForEach(catalog.entries) { entry in
                            ExtensionCard(entry: entry, pinned: pins.isPinned(entry)) {
                                pane.showExtension(entry.id)
                            } togglePin: {
                                pins.toggle(entry)
                            }
                        }
                    }
                }
                Text("Extensions on paired hosts come later.")
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.text2)
            }
            .padding(DesktopSpace.xxl)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { catalog.refresh() }
        .sheet(isPresented: $showsHistory) {
            ExtensionHistorySheet(api: model.api, extensionID: nil)
        }
        .sheet(isPresented: $showsNew) {
            NewExtensionSheet(api: model.api) { sessionID in pane.select(sessionID) }
        }
    }
}

private struct ExtensionCard: View {
    let entry: ExtensionEntry
    let pinned: Bool
    let open: () -> Void
    let togglePin: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(alignment: .top, spacing: DesktopSpace.m) {
                ExtensionIcon(name: entry.icon)
                    .font(.system(size: DesktopType.title, weight: .medium))
                    .foregroundStyle(DesktopPalette.text)
                    .frame(width: 34, height: 34)
                    .background(DesktopPalette.fill, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: DesktopSpace.xs) {
                        Text(entry.label)
                            .font(.system(size: DesktopType.body, weight: .medium))
                            .foregroundStyle(DesktopPalette.text)
                            .lineLimit(1)
                        if let badge = entry.badge {
                            Text(badge)
                                .font(.system(size: DesktopType.caption, weight: .semibold))
                                .monospacedDigit()
                                .foregroundStyle(DesktopPalette.attention)
                        }
                    }
                    if let detail = entry.unavailable ?? entry.detail {
                        Text(detail)
                            .font(.system(size: DesktopType.meta))
                            .foregroundStyle(DesktopPalette.text2)
                            .lineLimit(2)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(DesktopSpace.m)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(DesktopPalette.side, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
            .contentShape(Rectangle())
            .opacity(entry.url == nil ? 0.6 : 1)
        }
        .buttonStyle(.plain)
        .disabled(entry.url == nil)
        .accessibilityIdentifier("extension-entry-" + entry.id)
        .overlay(alignment: .topTrailing) {
            Button(action: togglePin) {
                Image(systemName: pinned ? "pin.fill" : "pin")
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(pinned || entry.offered ? DesktopPalette.text : DesktopPalette.text2)
            }
            .buttonStyle(.plain)
            .padding(DesktopSpace.s)
            .help(pinned ? "Unpin from the sidebar" : entry.offered ? "This extension asks for a sidebar entry. Pin it." : "Pin to the sidebar")
            .accessibilityLabel(pinned ? "Unpin \(entry.label)" : "Pin \(entry.label)")
            .accessibilityIdentifier("extension-pin-" + entry.id)
        }
        .contextMenu {
            Button(pinned ? "Unpin from Sidebar" : "Pin to Sidebar", action: togglePin)
        }
    }
}

struct ExtensionIcon: View {
    let name: String

    var body: some View {
        Image(systemName: NSImage(systemSymbolName: name, accessibilityDescription: nil) == nil ? ExtensionEntry.fallbackIcon : name)
    }
}

struct ExtensionPagePane: View {
    var model: AgenthailModel
    var pane: SessionPane
    let entryID: String

    var body: some View {
        let catalog = model.extensionCatalog
        Group {
            if let entry = catalog.entry(entryID) {
                if let url = entry.url {
                    let controller = ExtensionPages.controller(for: entry)
                    ExtensionPageHost(controller: controller, url: url, signIn: catalog.signIn)
                        .overlay {
                            if case .failed(let message) = controller.phase {
                                ContentUnavailableView {
                                    Label(entry.label, systemImage: "exclamationmark.triangle")
                                } description: {
                                    Text(message)
                                } actions: {
                                    Button("Reload") { controller.reload() }
                                }
                                .background(DesktopPalette.window)
                            }
                        }
                        .overlay(alignment: .topTrailing) { controls(entry) }
                } else {
                    gone(entry.label, entry.unavailable ?? "This page can't open right now.")
                }
            } else if catalog.list == nil {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                gone("This extension was removed", "It is no longer on \(catalog.hostName.isEmpty ? "this computer" : catalog.hostName).")
                    .onAppear { ExtensionPages.forget(entryID) }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .ignoresSafeArea(.all, edges: pane.pageFills ? .all : [])
        .background {
            if pane.pageFills {
                Button("Show sidebar and toolbar") { setFills(false) }
                    .keyboardShortcut(.cancelAction)
                    .opacity(0)
                    .frame(width: 0, height: 0)
                    .accessibilityHidden(true)
            }
        }
    }

    private func controls(_ entry: ExtensionEntry) -> some View {
        HStack(spacing: DesktopSpace.xs) {
            control("chevron.left", "All apps", id: "extension-back") { pane.showExtensions() }
            control("arrow.clockwise", "Reload", id: "extension-reload") { ExtensionPages.controller(for: entry).reload() }
            control(pane.pageFills ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right", pane.pageFills ? "Show sidebar and toolbar" : "Fill the window", id: "extension-full-screen") {
                setFills(!pane.pageFills)
            }
        }
        .padding(DesktopSpace.xs)
        .background(.regularMaterial, in: Capsule())
        .padding(DesktopSpace.s)
    }

    private func control(_ symbol: String, _ title: String, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: DesktopType.secondary, weight: .medium))
                .frame(width: 24, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(title)
        .accessibilityLabel(title)
        .accessibilityIdentifier(id)
    }

    private func setFills(_ fills: Bool) {
        pane.pageFills = fills
    }

    private func gone(_ title: String, _ detail: String) -> some View {
        ContentUnavailableView {
            Label(title, systemImage: ExtensionEntry.fallbackIcon)
        } description: {
            Text(detail)
        } actions: {
            Button("All Apps") { pane.showExtensions() }
        }
    }
}

private struct ExtensionPageHost: NSViewRepresentable {
    let controller: ExtensionPageController
    let url: URL
    let signIn: ExtensionSignIn?

    func makeNSView(context: Context) -> NSView {
        controller.openOutside = { NSWorkspace.shared.open($0) }
        let host = NSView()
        attach(to: host)
        return host
    }

    func updateNSView(_ host: NSView, context: Context) {
        if controller.webView.superview !== host { attach(to: host) }
        controller.open(url, signIn: signIn)
    }

    private func attach(to host: NSView) {
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

struct ExtensionHistorySheet: View {
    let api: AgenthailAPI?
    let extensionID: String?
    @Environment(\.dismiss) private var dismiss
    @State private var changes: [ExtensionChange]?
    @State private var failure: String?
    @State private var undoing: String?

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            HStack {
                Text("Extension History").font(.system(size: DesktopType.title, weight: .semibold))
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            Text("Changes are kept as they are made. Undo any change here.")
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.text2)
            if let failure {
                Text(failure).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text)
            }
            if let changes {
                if changes.isEmpty {
                    Text("No changes recorded yet.").foregroundStyle(DesktopPalette.text2)
                }
                List(changes) { change in
                    HStack(alignment: .top) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(ExtensionHistory.describe(change)).font(.system(size: DesktopType.body))
                            Text(change.time.formatted(date: .abbreviated, time: .shortened) + (change.author.map { " · \($0)" } ?? ""))
                                .font(.system(size: DesktopType.meta))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                        Spacer()
                        if change.root != true {
                            Button("Undo") { Task { await undo(change) } }
                                .disabled(undoing != nil)
                        }
                    }
                }
                .frame(minHeight: 280)
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 280)
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 520)
        .task { await load() }
    }

    private func load() async {
        guard let api else { return }
        do {
            let all = try await api.extensionChanges()
            changes = extensionID.map { id in all.filter { $0.touches(id) } } ?? all
        } catch {
            failure = error.localizedDescription
        }
    }

    private func undo(_ change: ExtensionChange) async {
        guard let api else { return }
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

struct NewExtensionSheet: View {
    let api: AgenthailAPI?
    let started: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var message = ""
    @State private var starting = false
    @State private var failure: String?

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text("New Extension").font(.system(size: DesktopType.title, weight: .semibold))
            Text("Describe what it should show or do. A builder session starts, and its changes are kept as it makes them.")
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.text2)
            TextEditor(text: $message)
                .font(.system(size: DesktopType.body))
                .frame(minHeight: 120)
                .accessibilityIdentifier("new-extension-message")
            if let failure {
                Text(failure).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text)
            }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Start Builder") { Task { await start() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(starting || message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(DesktopSpace.xl)
        .frame(width: 480)
    }

    private func start() async {
        guard let api else { return }
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
