import SwiftUI

struct ConnectionControls: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @EnvironmentObject private var voice: VoiceController
    @Environment(\.openWindow) private var openWindow

    private var offlineLabel: String {
        if model.reconnecting { return "Reconnecting…" }
        if model.daemonSlow { return "Agenthail is slow to respond" }
        if model.loading && model.connectionError == nil { return "Loading sessions…" }
        guard model.snapshot != nil, let loadedAt = model.snapshotLoadedAt else { return "Agenthail isn't running" }
        return "Offline · as of \(relativeAge(loadedAt.formatted(.iso8601)))"
    }

    private var syncedLabel: String {
        let age = model.snapshotLoadedAt.map { relativeAge($0.formatted(.iso8601)) } ?? ""
        let synced = age.isEmpty ? "Not synced yet" : age == "now" ? "Synced just now" : "Synced \(age) ago"
        return "\(synced). \(shortcuts.help("Refresh", .refresh))"
    }

    var body: some View {
        HStack(spacing: DesktopSpace.s) {
            Circle()
                .fill(model.isConnected ? DesktopPalette.done : DesktopPalette.failed)
                .frame(width: 6, height: 6)
                .help(model.isConnected ? "Connected to Agenthail on this Mac" : offlineLabel)
                .accessibilityLabel(model.isConnected ? "Connected" : offlineLabel)
            if !model.isConnected {
                Text(offlineLabel)
                    .lineLimit(1)
                    .layoutPriority(-1)
            }
            if model.isConnected, let notice = model.daemonBuildNotice {
                Button("Restart for this version") { model.restartDaemon() }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.failed)
                    .help(notice)
            }
            if !model.isConnected && !model.reconnecting && !model.daemonSlow {
                Button("Start") { model.restartDaemon() }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accentText)
                    .help("Start the Agenthail daemon")
            }
            if model.isConnected {
                Button {
                    voice.route(to: "")
                    openWindow(id: "voice")
                } label: {
                    Image(systemName: "waveform")
                }
                .buttonStyle(.plain)
                .help("Talk to orchestrator")
                .accessibilityLabel("Talk to orchestrator")
                Button { model.refreshNow() } label: {
                    Image(systemName: "arrow.triangle.2.circlepath")
                }
                .buttonStyle(.plain)
                .help(syncedLabel)
                .accessibilityLabel("Refresh")
                .accessibilityValue(syncedLabel)
            }
            SettingsLink {
                Image(systemName: "slider.horizontal.3")
            }
            .buttonStyle(.plain)
            .help("Settings ⌘,")
            .accessibilityLabel("Settings")
        }
    }
}
