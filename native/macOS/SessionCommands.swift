import SwiftUI

struct SessionCommands: Commands {
    @ObservedObject var model: AgenthailModel
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
            .keyboardShortcut("n", modifiers: .command)
        }
        CommandMenu("Session") {
            Button("Go to Session…") {
                showMainWindow()
                model.paletteVisible.toggle()
            }
            .keyboardShortcut("k", modifiers: .command)
            Divider()
            Button("Rename…") { pane?.renamingSession = session }
                .disabled(!writable)
            Button("Open in New Window") {
                if let id = session?.id { openWindow(id: "session", value: id) }
            }
            .disabled(session == nil)
            Divider()
            Button("Stop") { pane?.interrupt() }
                .keyboardShortcut(".", modifiers: .command)
                .disabled(!writable || session?.isWorking != true)
        }
        CommandGroup(before: .sidebar) {
            ForEach(SessionFilter.allCases) { filter in
                Button(filter.rawValue) {
                    showMainWindow()
                    model.sessionFilter = filter
                }
                .keyboardShortcut(KeyEquivalent(Character("\(filter.shortcut)")), modifiers: .command)
            }
            Divider()
            Button(pane?.inspectorVisible == true ? "Hide Inspector" : "Show Inspector") { pane?.inspectorVisible.toggle() }
                .keyboardShortcut("i", modifiers: [.command, .option])
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
    var shortcut: Int {
        switch self {
        case .running: return 1
        case .recent: return 2
        case .all: return 3
        }
    }
}
