import AppKit
import SwiftUI
import WebKit

struct ExtensionsPane: View {
    var model: AgenthailModel
    var pane: SessionPane
    @State private var history: HistoryTarget?
    @State private var building: ExtensionBuildTarget?
    private var pins: ExtensionPins { .shared }

    var body: some View {
        let shown = Self.sections(model.extensionCatalogs)
        let all = model.extensionCatalogs
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.l) {
                HStack(alignment: .firstTextBaseline) {
                    Text("Apps")
                        .font(.system(size: DesktopType.display, weight: .semibold))
                        .foregroundStyle(DesktopPalette.text)
                    Spacer()
                    hostMenu("History", catalogs: all, id: "extensions-history") { history = HistoryTarget($0) }
                    hostMenu("New App…", catalogs: all, id: "extensions-new", blocked: ExtensionBuildTarget.unavailable) { building = ExtensionBuildTarget($0) }
                }
                ForEach(shown, id: \.hostID) { catalog in
                    if shown.count > 1 {
                        Text(catalog === model.extensionCatalog ? "On this Mac" : "On \(catalog.hostName)")
                            .font(.system(size: DesktopType.title, weight: .semibold))
                            .foregroundStyle(DesktopPalette.text)
                            .padding(.top, DesktopSpace.m)
                    }
                    entries(catalog, empty: catalog === model.extensionCatalog ? "No apps on this Mac yet. Use New App to describe one." : "No apps on \(catalog.hostName).")
                }
            }
            .padding(DesktopSpace.xxl)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { model.extensionCatalogs.forEach { $0.refresh() } }
        .onAppear { model.extensionsHubsShown += 1 }
        .onDisappear { model.extensionsHubsShown -= 1 }
        .sheet(item: $history) { target in
            ExtensionHistorySheet(api: target.api, host: target.host, hostName: target.hostName, extensionID: nil)
        }
        .sheet(item: $building) { target in
            NewExtensionSheet(api: model.api, target: target) { sessionID in
                pane.select(sessionID)
                model.extensionCatalogs.first { $0.hostID == target.id }?.refresh()
            }
        }
    }

    static func sections(_ catalogs: [ExtensionCatalog]) -> [ExtensionCatalog] {
        let states = catalogs.map { ExtensionSections.State(apps: !$0.entries.isEmpty, failed: $0.error != nil, pending: $0.list == nil) }
        return ExtensionSections.shown(states).map { catalogs[$0] }
    }

    @ViewBuilder
    private func hostMenu(_ title: String, catalogs: [ExtensionCatalog], id: String, blocked: @escaping (ExtensionCatalog) -> String? = { _ in nil }, action: @escaping (ExtensionCatalog) -> Void) -> some View {
        if catalogs.count == 1, let only = catalogs.first {
            Button(title) { action(only) }
                .disabled(blocked(only) != nil)
                .help(blocked(only) ?? "")
                .accessibilityIdentifier(id)
        } else {
            Menu(title) {
                ForEach(catalogs, id: \.hostID) { catalog in
                    Button(catalog === model.extensionCatalog ? "On This Mac" : "On \(catalog.hostName)") { action(catalog) }
                        .disabled(blocked(catalog) != nil)
                        .help(blocked(catalog) ?? "")
                }
            }
            .fixedSize()
            .accessibilityIdentifier(id)
        }
    }

    @ViewBuilder
    private func entries(_ catalog: ExtensionCatalog, empty: String) -> some View {
        if let error = catalog.error {
            Text(error)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
        }
        if catalog.entries.isEmpty {
            if catalog.error == nil {
                Text(catalog.list == nil ? "Looking for extensions…" : empty)
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
            }
        } else {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 180, maximum: 260), spacing: DesktopSpace.m, alignment: .top)], alignment: .leading, spacing: DesktopSpace.m) {
                ForEach(catalog.entries) { entry in
                    ExtensionCard(entry: entry, pinned: pins.isPinned(entry)) {
                        if let session = entry.approvalSession {
                            pane.select(session)
                        } else {
                            pane.showExtension(entry.id)
                        }
                    } togglePin: {
                        pins.toggle(entry)
                    }
                }
            }
        }
    }
}

private struct HistoryTarget: Identifiable {
    let id: String
    let api: AgenthailAPI?
    let host: String?
    let hostName: String

    @MainActor init(_ catalog: ExtensionCatalog) {
        id = catalog.hostID
        api = catalog.api
        host = catalog.hostRef
        hostName = catalog.hostName
    }
}

private struct ExtensionCard: View {
    let entry: ExtensionEntry
    let pinned: Bool
    let open: () -> Void
    let togglePin: () -> Void

    static func icon(_ entry: ExtensionEntry) -> String {
        guard entry.icon == ExtensionEntry.fallbackIcon else { return entry.icon }
        if case .mount = entry.kind { return "globe" }
        return "square.grid.2x2"
    }

    static func subtitle(_ entry: ExtensionEntry) -> String {
        if let unavailable = entry.unavailable { return unavailable }
        if case .mount = entry.kind { return "Web app, part of \(entry.extensionLabel)" }
        return entry.detail ?? entry.extensionLabel
    }

    var body: some View {
        Button(action: open) {
            HStack(alignment: .top, spacing: DesktopSpace.m) {
                ExtensionIcon(name: Self.icon(entry))
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
                    Text(Self.subtitle(entry))
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(entry.awaitsApproval ? DesktopPalette.attention : DesktopPalette.text2)
                        .lineLimit(2, reservesSpace: true)
                    Text("on \(entry.hostName)")
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.text2)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
            }
            .padding(DesktopSpace.m)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(DesktopPalette.side, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(entry.approvalSession == nil ? "" : "Open the request to approve \(entry.extensionLabel)")
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
        let catalog = model.extensionCatalog(forEntry: entryID) ?? model.extensionCatalog
        Group {
            if let entry = catalog.entry(entryID) {
                if let url = entry.url {
                    let controller = ExtensionPages.controller(for: entry)
                    VStack(spacing: 0) {
                        if !pane.pageFills { titleBand(entry, controller: controller) }
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
                            .overlay(alignment: .topTrailing) {
                                if pane.pageFills {
                                    HStack(spacing: DesktopSpace.s) {
                                        ExtensionSlowNote(phase: controller.phase, since: controller.waitingSince)
                                        controls(entry)
                                    }
                                }
                            }
                    }
                } else {
                    gone(entry.label, entry.awaitsApproval && entry.approvalSession == nil
                        ? "It needs your OK to open, and nothing has asked for it yet. On \(entry.hostName), approve it with agenthail extensions approve \(entry.extensionID)."
                        : entry.unavailable ?? "This page can't open right now.")
                }
            } else if catalog.list == nil {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                gone("This extension was removed", "It is no longer on \(catalog.hostName.isEmpty ? "this computer" : catalog.hostName).")
                    .onAppear { ExtensionPages.forget(entryID) }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task(id: entryID) { if catalog.list == nil && !catalog.loading { catalog.refresh() } }
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

    private func titleBand(_ entry: ExtensionEntry, controller: ExtensionPageController) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
            Text(entry.label)
                .font(.system(size: DesktopType.title, weight: .semibold))
                .foregroundStyle(DesktopPalette.text)
            Text("on \(entry.hostName)")
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
            ExtensionSlowNote(phase: controller.phase, since: controller.waitingSince)
            Spacer(minLength: DesktopSpace.s)
            controls(entry)
        }
        .lineLimit(1)
        .padding(.leading, DesktopSpace.l)
        .frame(height: 44)
        .overlay(alignment: .bottom) { Rectangle().fill(DesktopPalette.line2).frame(height: 1) }
        .accessibilityIdentifier("extension-title")
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

struct ExtensionPageHost: NSViewRepresentable {
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
    var host: String? = nil
    var hostName = ""
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
            Text(host == nil ? "Changes are kept as they are made. Undo any change here." : "Changes on \(hostName). Undo any change here.")
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
            let all = try await api.extensionChanges(host: host)
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
            try await api.undoExtensionChange(change.rev, host: host)
            failure = nil
            await load()
        } catch {
            failure = error.localizedDescription
        }
    }
}

struct NewExtensionSheet: View {
    let api: AgenthailAPI?
    let target: ExtensionBuildTarget
    let started: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var message = ""
    @State private var starting = false
    @State private var failure: String?
    @State private var buildKey = ExtensionBuildKey()

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            Text(target.title).font(.system(size: DesktopType.title, weight: .semibold))
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
        .onChange(of: message) { buildKey.reset() }
    }

    private func start() async {
        guard let api else { return }
        starting = true
        defer { starting = false }
        do {
            let created = try await api.buildExtension(message.trimmingCharacters(in: .whitespacesAndNewlines), host: target.host, idempotencyKey: buildKey.current())
            buildKey.reset()
            dismiss()
            if let id = created.session?.id { started(id) }
        } catch {
            buildKey.failed(error)
            failure = error.localizedDescription
        }
    }
}

private struct ExtensionSlowNote: View {
    let phase: ExtensionPageController.Phase
    let since: Date?
    @State private var slow = false

    private var waiting: Bool { phase.waiting }

    var body: some View {
        Group {
            if waiting && slow {
                HStack(spacing: DesktopSpace.xs) {
                    ProgressView().controlSize(.mini)
                    Text(phase == .signingIn ? "Still signing in…" : "Still loading…")
                }
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
            }
        }
        .task(id: waiting) {
            slow = false
            guard waiting else { return }
            let remaining = 3 - Date().timeIntervalSince(since ?? Date())
            if remaining > 0 { try? await Task.sleep(for: .seconds(remaining)) }
            if !Task.isCancelled { slow = true }
        }
    }
}
