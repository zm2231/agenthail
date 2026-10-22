import AppKit
import SwiftUI

struct OverviewSettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        Form {
            if let snapshot = model.snapshot {
                Section {
                    Text(OverviewSummary.line(snapshot))
                        .foregroundStyle(snapshot.daemon.stale == true ? DesktopPalette.failed : DesktopPalette.text2)
                }
                PermissionsSection(model: model)
                Section("Surfaces") {
                    let cards = SurfaceOverview.build(surfaces: snapshot.surfaces, sessions: snapshot.sessions, codexRecentHours: snapshot.codexRecentHours)
                    if cards.isEmpty {
                        Text("No surfaces are configured.").foregroundStyle(DesktopPalette.text2)
                    }
                    ForEach(cards) { SurfaceCard(model: model, overview: $0) }
                }
            } else {
                Section { ProgressView().controlSize(.small) }
            }
            SettingsError(model: model)
        }
        .formStyle(.grouped)
    }
}

/// The privacy grants the daemon holds, each with the System Settings pane
/// that changes it. Agents the daemon runs, agent-hands among them, use them.
private struct PermissionsSection: View {
    @ObservedObject var model: AgenthailModel
    @State private var asking = false

    var body: some View {
        Group {
            if let state = model.permissions, state.supported {
                Section {
                    if let problem = state.problem {
                        Text(problem).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.failed).textSelection(.enabled)
                    }
                    ForEach(state.grants) { grant in
                        HStack(spacing: DesktopSpace.s) {
                            Text(grant.title).font(.system(size: DesktopType.body))
                            Label(grant.stateLabel, systemImage: "circle.fill")
                                .labelStyle(StatusDotLabelStyle())
                                .font(.system(size: DesktopType.secondary))
                                .foregroundStyle(grant.state == "granted" ? DesktopPalette.done : DesktopPalette.failed)
                            Spacer()
                            if grant.state != "granted", let url = URL(string: grant.url) {
                                Button("Open Settings") { NSWorkspace.shared.open(url) }
                                    .help(grant.pane)
                            }
                        }
                    }
                    if state.grants.contains(where: { $0.state == "missing" }) {
                        HStack {
                            Text("macOS lists Agenthail Daemon in these panes once it has asked.")
                                .font(.system(size: DesktopType.secondary))
                                .foregroundStyle(DesktopPalette.text2)
                            Spacer()
                            if asking { ProgressView().controlSize(.small) }
                            Button("Ask macOS") {
                                asking = true
                                Task {
                                    await model.requestPermissions()
                                    asking = false
                                }
                            }
                            .disabled(asking)
                        }
                    }
                } header: {
                    Text("Privacy Permissions")
                } footer: {
                    if let holder = state.holder {
                        Text("Held by \(holder)").font(.system(size: DesktopType.caption)).foregroundStyle(DesktopPalette.text2).textSelection(.enabled)
                    }
                }
            } else if let error = model.permissionsError {
                Section("Privacy Permissions") {
                    Text(error).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.failed).textSelection(.enabled)
                }
            }
        }
        .task { await model.loadPermissions() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await model.loadPermissions() }
        }
    }
}

private struct SurfaceCard: View {
    @ObservedObject var model: AgenthailModel
    let overview: SurfaceOverview
    @State private var repairing = false
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            HStack(spacing: DesktopSpace.s) {
                Text(overview.name).font(.system(size: DesktopType.body, weight: .semibold))
                Label(overview.stateLabel, systemImage: "circle.fill")
                    .labelStyle(StatusDotLabelStyle())
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(color)
                Spacer()
                if let label = overview.surface.repairLabel, overview.surface.repairAction != nil {
                    if repairing { ProgressView().controlSize(.small) }
                    Button(label) {
                        repairing = true
                        Task {
                            await model.repairSurface(overview.surface)
                            repairing = false
                        }
                    }
                    .disabled(repairing)
                }
                Button("Show Sessions") { showSessions() }
                    .help("Show every \(overview.name) session in the main window")
            }
            HStack(alignment: .top, spacing: 22) {
                stat("Working", overview.working, note: nil)
                stat("Current", overview.current, note: overview.currentWindow)
                stat("Queued", overview.queued, note: nil)
            }
            Text(overview.detail)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(overview.health == .healthy ? DesktopPalette.text2 : DesktopPalette.failed)
                .textSelection(.enabled)
            if let availability = overview.availability {
                Text(availability).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
            }
            ForEach(overview.advice, id: \.self) { line in
                Text(line)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .textSelection(.enabled)
            }
        }
        .padding(.vertical, DesktopSpace.xs)
    }

    private func showSessions() {
        model.search("")
        model.sessionFilter = .all
        model.sessionRefinement.surface = overview.surface.name
        NSApplication.shared.activate()
        openWindow(id: "main")
    }

    private var color: Color {
        switch overview.health {
        case .healthy: return DesktopPalette.done
        case .degraded: return DesktopPalette.failed
        case .signinOff, .signinUnavailable: return DesktopPalette.text2
        case .unavailable: return DesktopPalette.failed
        }
    }

    private func stat(_ title: String, _ value: Int, note: String?) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title).font(.system(size: DesktopType.caption)).foregroundStyle(DesktopPalette.text2)
            Text("\(value)").font(.system(size: DesktopType.title, weight: .medium)).monospacedDigit()
            if let note {
                Text(note).font(.system(size: DesktopType.caption)).foregroundStyle(DesktopPalette.muted)
            }
        }
    }
}

private struct StatusDotLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: DesktopSpace.xs) {
            configuration.icon.font(.system(size: 7))
            configuration.title
        }
    }
}

struct RecentOutcomesSection: View {
    let history: [HistoryState]

    var body: some View {
        Section("Recent outcomes") {
            let outcomes = DeliveryOutcome.recent(history)
            if outcomes.isEmpty {
                Text("No delivery outcomes have been recorded yet.").foregroundStyle(DesktopPalette.text2)
            }
            ForEach(outcomes) { outcome in
                VStack(alignment: .leading, spacing: 2) {
                    HStack {
                        Text("\(outcome.label) · \(outcome.target)")
                            .foregroundStyle(outcome.tone == .problem ? DesktopPalette.failed : DesktopPalette.text)
                            .textSelection(.enabled)
                        Spacer()
                        Text(relativeAge(outcome.createdAt))
                            .font(.system(size: DesktopType.caption))
                            .foregroundStyle(DesktopPalette.text2)
                            .help(outcome.createdAt)
                    }
                    Text(outcome.detail)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(outcome.isError ? DesktopPalette.failed : DesktopPalette.text2)
                        .textSelection(.enabled)
                }
            }
        }
    }
}

struct BrowserAccessSection: View {
    let remote: RemoteAccessState
    @State private var qrVisible = false
    @State private var hideTask: Task<Void, Never>?
    @State private var copied = false

    var body: some View {
        if remote.enabled, let link = remote.url {
            Section {
                HStack(alignment: .top, spacing: 14) {
                    if qrVisible, let image = QRCodeImage.make(link) {
                        Image(nsImage: image).interpolation(.none).resizable().frame(width: 128, height: 128)
                    }
                    VStack(alignment: .leading, spacing: 6) {
                        Text(qrVisible ? remote.dnsName ?? "Browser link" : "Browser link hidden")
                        Text("The link signs in to this Agenthail. Share it only with your own devices. On iPhone, open it, tap Share, then Add to Home Screen.")
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(DesktopPalette.text2)
                        HStack {
                            Button(copied ? "Copied" : "Copy Browser Link") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(link, forType: .string)
                                copied = true
                                Task {
                                    try? await Task.sleep(for: .seconds(2))
                                    copied = false
                                }
                            }
                            Button(qrVisible ? "Hide QR Code" : "Show QR Code") { qrVisible ? hide() : reveal() }
                        }
                    }
                }
            } header: {
                Text("Open in a browser")
            }
            .onDisappear(perform: hide)
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didResignActiveNotification)) { _ in hide() }
        }
    }

    private func reveal() {
        hideTask?.cancel()
        qrVisible = true
        hideTask = Task {
            try? await Task.sleep(for: .seconds(60))
            if !Task.isCancelled { qrVisible = false }
        }
    }

    private func hide() {
        hideTask?.cancel()
        hideTask = nil
        qrVisible = false
        copied = false
    }
}
