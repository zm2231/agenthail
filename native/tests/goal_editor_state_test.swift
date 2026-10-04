import Foundation

@main
struct GoalEditorStateTest {
    static func main() {
        var editor = GoalEditorState()
        for mode in [GoalEditorState.Mode.newGoal, .editGoal, .budget] {
            editor.begin(mode, sessionID: "A")
            guard editor.canCommit(currentSessionID: "A") else { exit(1) }
            editor.select(sessionID: "B")
            guard !editor.canCommit(currentSessionID: "B"), editor.mode == nil, editor.sessionID == nil else { exit(1) }
        }
        editor.begin(.editGoal, sessionID: "A")
        editor.reset()
        guard editor.mode == nil, editor.sessionID == nil else { exit(1) }
    }
}
