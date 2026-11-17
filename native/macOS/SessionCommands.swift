import SwiftUI

struct SessionCommands: Commands {
    var model: AgenthailModel
    let voice: VoiceController
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @FocusedValue(SessionPane.self) private var pane
    @Environment(\.openWindow) private var openWindow
    @AppStorage(AgentFold.defaultsKey) private var expandAgents = false
    @AppStorage(TranscriptFocus.defaultsKey) private var focus = false
    @AppStorage(SessionListLayout.groupsKey) private var groupsByProject = true

    private var session: SessionState? { pane?.displayedSession }
    private var tabs: Bool { pane?.tabsEnabled == true }
    private var writable: Bool { session.map { !$0.isReadOnly } == true && pane?.removedSession == nil }

    var body: some Commands {
        CommandGroup(replacing: .newItem) {
            Button("New Session") {
                showMainWindow()
                model.openNewSession()
            }
            .keyboardShortcut(shortcuts.keyboardShortcut(.newSession))
            if tabs {
                Button("New Tab") {
                    showMainWindow()
                    model.openNewSession()
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
        CommandGroup(after: .help) {
            Button("Save Transcript Trace") { TranscriptTrace.saveAndReveal() }
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
            Button("Move to Another Computer…") { if let session { pane?.movingSession = session } }
                .disabled(session.map(model.canMove) != true || pane?.removedSession != nil)
            Button(session?.isPinned == true ? "Unpin" : "Pin") { if let session { model.setPinned(sessionID: session.id, !session.isPinned) } }
                .disabled(session == nil || pane?.removedSession != nil)
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
            Toggle("Group by Project", isOn: $groupsByProject)
            Toggle("Show Archived", isOn: Binding(get: { model.showsArchived }, set: { model.showsArchived = $0 }))
                .disabled(model.sessionFilter != .all)
            Divider()
            Button(pane?.sidebarOnScreen == false ? "Show Sidebar" : "Hide Sidebar") { pane?.toggleSidebar() }
                .keyboardShortcut(shortcuts.keyboardShortcut(.toggleSidebar))
                .disabled(pane == nil)
            Button(pane?.inspectorShown == true ? "Hide Inspector" : "Show Inspector") { pane?.toggleInspector() }
                .keyboardShortcut(shortcuts.keyboardShortcut(.toggleInspector))
                .disabled(pane == nil)
            Toggle("Expand Agent Messages", isOn: $expandAgents)
                .keyboardShortcut(shortcuts.keyboardShortcut(.expandAgentMessages))
            Toggle("Focus on Messages", isOn: $focus)
            Button("Make Text Bigger") { model.interface.stepTextScale(by: 1, api: model.api) }
                .keyboardShortcut("+", modifiers: .command)
            Button("Make Text Smaller") { model.interface.stepTextScale(by: -1, api: model.api) }
                .keyboardShortcut("-", modifiers: .command)
            Button("Actual Text Size") { model.interface.stepTextScale(by: nil, api: model.api) }
                .keyboardShortcut("0", modifiers: .command)
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
        MainWindowShow.show(model.mainWindow) { openWindow(id: "main") }
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
