import SwiftUI

struct SessionCommands: Commands {
    @ObservedObject var model: AgenthailModel
    let voice: VoiceController
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @FocusedObject private var pane: SessionPane?
    @Environment(\.openWindow) private var openWindow

    private var session: SessionState? { pane?.displayedSession }
    private var tabs: Bool { pane?.tabsEnabled == true }
    private var writable: Bool { session.map { !$0.isReadOnly } == true && pane?.removedSession == nil }

    var body: some Commands {
        CommandGroup(replacing: .newItem) {
            Button("New Session…") {
                showMainWindow()
                model.newSessionVisible = true
            }
            .keyboardShortcut(shortcuts.keyboardShortcut(.newSession))
            if tabs {
                Button("New Tab") {
                    showMainWindow()
                    model.newSessionVisible = true
                }
                .keyboardShortcut(shortcuts.keyboardShortcut(.newTab))
            }
        }
        CommandGroup(replacing: .saveItem) {
            if tabs, let pane, let active = pane.tabs.active {
                Button("Close Tab") { pane.closeTab(active) }
                    .keyboardShortcut(shortcuts.keyboardShortcut(.closeTab))
            } else {
                Button("Close Window") { NSApp.keyWindow?.performClose(nil) }
                    .keyboardShortcut("w")
            }
        }
        CommandMenu("Session") {
            Button("Go to Session…") {
                showMainWindow()
                model.paletteVisible.toggle()
            }
            .keyboardShortcut(shortcuts.keyboardShortcut(.goToSession))
            Divider()
            Button("Rename…") { pane?.renamingSession = session }
                .keyboardShortcut(shortcuts.keyboardShortcut(.rename))
                .disabled(!writable)
            Button("Open in New Window") {
                if let id = session?.id { openWindow(id: "session", value: id) }
            }
            .keyboardShortcut(shortcuts.keyboardShortcut(.openInNewWindow))
            .disabled(session == nil)
            Button("Open in Agenthail") { if let session { pane?.adoptingSession = session } }
                .disabled(session?.canAdopt != true || pane?.removedSession != nil)
            Button("Fork") { if let session { pane?.fork(session) } }
                .disabled(session.map(SessionOperationAvailability.fork) != true || pane?.removedSession != nil || pane?.forking == true)
            if tabs, let pane {
                Divider()
                Button("Next Tab") { pane.stepTab(1) }
                    .keyboardShortcut(shortcuts.keyboardShortcut(.nextTab))
                    .disabled(pane.tabs.tabs.count < 2)
                Button("Previous Tab") { pane.stepTab(-1) }
                    .keyboardShortcut(shortcuts.keyboardShortcut(.previousTab))
                    .disabled(pane.tabs.tabs.count < 2)
                Button("Keep Tab Open") { if let id = pane.tabs.preview { pane.keepTab(id) } }
                    .keyboardShortcut(shortcuts.keyboardShortcut(.keepTab))
                    .disabled(pane.tabs.preview == nil || pane.tabs.preview != pane.tabs.active)
            }
            Divider()
            Button("Talk to Orchestrator…") { openVoice("") }
            Button("Call This Session…") { if let id = session?.id { openVoice(id) } }
                .disabled(session?.canCallByVoice != true || pane?.removedSession != nil)
            Divider()
            Button("Stop") {
                if let pane, pane.canStop { pane.interrupt() }
            }
            .keyboardShortcut(shortcuts.keyboardShortcut(.stop))
            .disabled(pane?.canStop != true)
        }
        CommandGroup(before: .sidebar) {
            ForEach(SessionFilter.allCases) { filter in
                Button(filter.rawValue) {
                    showMainWindow()
                    model.sessionFilter = filter
                }
                .keyboardShortcut(shortcuts.keyboardShortcut(filter.command))
            }
            Divider()
            Button(pane?.inspectorVisible == true ? "Hide Inspector" : "Show Inspector") { pane?.inspectorVisible.toggle() }
                .keyboardShortcut(shortcuts.keyboardShortcut(.toggleInspector))
                .disabled(pane == nil)
            Button("Refresh") { model.refreshNow(pane) }
                .keyboardShortcut(shortcuts.keyboardShortcut(.refresh))
                .disabled(model.api == nil)
            Divider()
        }
    }

    private func openVoice(_ targetID: String) {
        voice.route(to: targetID)
        NSApplication.shared.activate()
        openWindow(id: "voice")
    }

    private func showMainWindow() {
        NSApplication.shared.activate()
        openWindow(id: "main")
    }
}

extension SessionFilter {
    var command: AppCommand {
        switch self {
        case .running: return .showRunning
        case .recent: return .showRecent
        case .all: return .showAll
        case .archived: return .showArchived
        }
    }
}
