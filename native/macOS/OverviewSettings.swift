import AppKit
import SwiftUI

struct OverviewSettings: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        Form {
            if let snapshot = model.snapshot {
                Section {
                    Text(OverviewSummary.line(snapshot))
                        .foregroundStyle(snapshot.daemon.stale == true ? DesktopPalette.amber : DesktopPalette.text2)
                }
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

private struct SurfaceCard: View {
    @ObservedObject var model: AgenthailModel
    let overview: SurfaceOverview
    @State private var repairing = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Text(overview.name).font(.system(size: 13, weight: .semibold))
                Label(overview.stateLabel, systemImage: "circle.fill")
                    .labelStyle(StatusDotLabelStyle())
                    .font(.system(size: 12))
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
            }
            HStack(alignment: .top, spacing: 22) {
                stat("Working", overview.working, note: nil)
                stat("Current", overview.current, note: overview.currentWindow)
                stat("Queued", overview.queued, note: nil)
            }
            Text(overview.detail)
                .font(.system(size: 12))
                .foregroundStyle(overview.health == .healthy ? DesktopPalette.text2 : DesktopPalette.amber)
                .textSelection(.enabled)
            if let availability = overview.availability {
                Text(availability).font(.system(size: 12)).foregroundStyle(DesktopPalette.text2)
            }
            ForEach(overview.advice, id: \.self) { line in
                Text(line)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.text2)
                    .textSelection(.enabled)
            }
        }
        .padding(.vertical, 4)
    }

    private var color: Color {
        switch overview.health {
        case .healthy: return DesktopPalette.green
        case .degraded: return DesktopPalette.amber
        case .unavailable: return DesktopPalette.red
        }
    }

    private func stat(_ title: String, _ value: Int, note: String?) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title).font(.system(size: 11)).foregroundStyle(DesktopPalette.text2)
            Text("\(value)").font(.system(size: 15, weight: .medium)).monospacedDigit()
            if let note {
                Text(note).font(.system(size: 10.5)).foregroundStyle(DesktopPalette.muted)
            }
        }
    }
}

private struct StatusDotLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 4) {
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
                            .foregroundStyle(outcome.tone == .problem ? DesktopPalette.red : DesktopPalette.text)
                            .lineLimit(1)
                        Spacer()
                        Text(relativeAge(outcome.createdAt))
                            .font(.system(size: 11))
                            .foregroundStyle(DesktopPalette.text2)
                            .help(outcome.createdAt)
                    }
                    Text(outcome.isError ? outcome.detail : PeerEnvelope(outcome.detail).summary)
                        .font(.system(size: 12))
                        .foregroundStyle(outcome.isError ? DesktopPalette.red : DesktopPalette.text2)
                        .lineLimit(2)
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
                            .font(.system(size: 12))
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
