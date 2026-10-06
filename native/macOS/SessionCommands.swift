import SwiftUI

struct SessionCommands: Commands {
    @ObservedObject var model: AgenthailModel
    @ObservedObject private var shortcuts = ShortcutStore.shared
    @FocusedObject private var pane: SessionPane?
    @Environment(\.openWindow) private var openWindow

    private var session: SessionState? { pane?.displayedSession }
    private var writable: Bool { session.map { !$0.isReadOnly } == true && pane?.removedSession == nil }

    var body: some Commands {
        CommandGroup(replacing: .newItem) {
            Button("New Session…") {
                showMainWindow()
                model.newSessionVisible = true
            }
            .keyboardShortcut(shortcuts.keyboardShortcut(.newSession))
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
            Button("Fork…") { pane?.forkingSession = session }
                .disabled(session.map(SessionOperationAvailability.fork) != true || pane?.removedSession != nil)
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
            Divider()
        }
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
        }
    }
}
