import Carbon.HIToolbox

// The other Agenthail app shares keyboardShortcuts; these are its keys this app has no command for.
enum SharedAppShortcuts {
    struct Command {
        let id: String
        let title: String
        let defaultShortcut: StoredShortcut?
    }

    static let commands: [Command] = [
        Command(id: "talk", title: "Talk to orchestrator", defaultShortcut: StoredShortcut(carbonKeyCode: kVK_ANSI_T, carbonModifiers: cmdKey | optionKey)),
        Command(id: "textBigger", title: "Make text bigger", defaultShortcut: StoredShortcut(carbonKeyCode: kVK_ANSI_Equal, carbonModifiers: cmdKey)),
        Command(id: "textSmaller", title: "Make text smaller", defaultShortcut: StoredShortcut(carbonKeyCode: kVK_ANSI_Minus, carbonModifiers: cmdKey)),
        Command(id: "textActual", title: "Actual text size", defaultShortcut: StoredShortcut(carbonKeyCode: kVK_ANSI_0, carbonModifiers: cmdKey)),
    ]

    static let fixed: [(StoredShortcut, String)] = [
        (StoredShortcut(carbonKeyCode: kVK_ANSI_Comma, carbonModifiers: cmdKey), "Settings"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_S, carbonModifiers: cmdKey | shiftKey), "Sessions"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_N, carbonModifiers: cmdKey | shiftKey), "Now"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_J, carbonModifiers: cmdKey), "Next session that needs you"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_1, carbonModifiers: cmdKey), "Hands-on session 1"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_2, carbonModifiers: cmdKey), "Hands-on session 2"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_3, carbonModifiers: cmdKey), "Hands-on session 3"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_4, carbonModifiers: cmdKey), "Hands-on session 4"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_5, carbonModifiers: cmdKey), "Hands-on session 5"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_B, carbonModifiers: cmdKey | optionKey), "Show or hide the brief"),
        (StoredShortcut(carbonKeyCode: kVK_Tab, carbonModifiers: controlKey), "Next hands-on session"),
        (StoredShortcut(carbonKeyCode: kVK_Tab, carbonModifiers: controlKey | shiftKey), "Previous hands-on session"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_1, carbonModifiers: cmdKey | optionKey), "Show or hide the first pane"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_2, carbonModifiers: cmdKey | optionKey), "Show or hide the second pane"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_3, carbonModifiers: cmdKey | optionKey), "Show or hide the third pane"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_1, carbonModifiers: cmdKey | optionKey | shiftKey), "Focus the first pane"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_2, carbonModifiers: cmdKey | optionKey | shiftKey), "Focus the second pane"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_3, carbonModifiers: cmdKey | optionKey | shiftKey), "Focus the third pane"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_Q, carbonModifiers: cmdKey), "Quit Agenthail"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_H, carbonModifiers: cmdKey), "Hide Agenthail"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_M, carbonModifiers: cmdKey), "Minimize"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_C, carbonModifiers: cmdKey), "Copy"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_X, carbonModifiers: cmdKey), "Cut"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_V, carbonModifiers: cmdKey), "Paste"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_A, carbonModifiers: cmdKey), "Select all"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_Z, carbonModifiers: cmdKey), "Undo"),
        (StoredShortcut(carbonKeyCode: kVK_ANSI_Z, carbonModifiers: cmdKey | shiftKey), "Redo"),
    ]

    static let thisAppOnly: Set<AppCommand> = [.showRunning, .showRecent, .showAll, .showArchived, .toggleSidebar, .expandAgentMessages, .send, .sendAlternate, .rename]
}
