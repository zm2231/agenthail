import SwiftUI

struct GoalSection: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState
    @State private var editor = GoalEditorState()
    @State private var text = ""

    private var editable: Bool {
        !session.isReadOnly && session.capabilities.goal && pane.removedSession == nil
    }

    var body: some View {
        Group {
            if let goal = pane.detail?.goal, !goal.objective.isEmpty {
                goalView(goal)
            } else if editable {
                Button("Set goal") { begin(.newGoal, text: "") }
                    .controlSize(.small)
            }
        }
        .sheet(isPresented: Binding(get: { editor.mode != nil }, set: { if !$0 { reset() } })) {
            editorSheet
        }
        .onChange(of: pane.selectedSessionID) {
            if pane.selectedSessionID != editor.sessionID { reset() }
        }
    }

    private func goalView(_ goal: GoalState) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                SidebarCaption("Goal").padding(.horizontal, -8)
                Spacer()
                Label(goal.displayStatus, systemImage: goal.needsAttention ? "exclamationmark.triangle.fill" : "target")
                    .font(.system(size: 11))
                    .foregroundStyle(goal.needsAttention ? DesktopPalette.amber : DesktopPalette.text2)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(DesktopPalette.selection, in: Capsule())
            }
            Text(goal.objective)
                .textSelection(.enabled)
            let usage = GoalSection.usage(goal)
            if !usage.isEmpty {
                Text(usage)
                    .font(.system(size: 11).monospacedDigit())
                    .foregroundStyle(DesktopPalette.text2)
            }
            if editable {
                HStack(spacing: 8) {
                    Button("Edit") { begin(.editGoal, text: goal.objective) }
                    if goal.status == "active" {
                        Button("Pause") { model.perform(action: "goal-pause", sessionID: session.id) }
                    } else if goal.status == "paused" {
                        Button("Resume") { model.perform(action: "goal-resume", sessionID: session.id) }
                    }
                    Button(goal.tokenBudget == nil ? "Set budget" : "Budget") { begin(.budget, text: goal.tokenBudget.map(String.init) ?? "") }
                    Button("Clear", role: .destructive) { model.perform(action: "goal-clear", sessionID: session.id) }
                }
                .controlSize(.small)
            }
        }
    }

    private var editorSheet: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(editor.mode == .budget ? "Token budget" : editor.mode == .editGoal ? "Edit goal" : "Set goal")
                .font(.headline)
            if editor.mode == .budget {
                TextField("Tokens", text: $text)
                    .textFieldStyle(.roundedBorder)
                Text("Leave empty to remove the budget.")
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.text2)
            } else {
                TextEditor(text: $text)
                    .font(.body)
                    .frame(minHeight: 100)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(DesktopPalette.line))
            }
            HStack {
                Spacer()
                Button("Cancel", role: .cancel) { reset() }
                    .keyboardShortcut(.cancelAction)
                Button("Save") { save() }
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(!canSave)
            }
        }
        .padding(20)
        .frame(width: 400)
    }

    private var canSave: Bool {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if editor.mode == .budget { return trimmed.isEmpty ? pane.detail?.goal?.tokenBudget != nil : (Int64(trimmed).map { $0 >= 0 } ?? false) }
        return !trimmed.isEmpty
    }

    private func begin(_ mode: GoalEditorState.Mode, text: String) {
        self.text = text
        editor.begin(mode, sessionID: session.id)
    }

    private func save() {
        guard canSave, editor.canCommit(currentSessionID: pane.selectedSessionID), let sessionID = editor.sessionID else { return reset() }
        let action: String
        switch editor.mode {
        case .newGoal: action = "goal-set"
        case .editGoal: action = "goal-edit"
        case .budget: action = "goal-budget"
        case nil: return reset()
        }
        model.perform(action: action, sessionID: sessionID, message: text.trimmingCharacters(in: .whitespacesAndNewlines))
        reset()
    }

    private func reset() {
        editor.reset()
        text = ""
    }

    static func usage(_ goal: GoalState) -> String {
        var parts: [String] = []
        if let seconds = goal.timeUsedSeconds { parts.append("\(duration(seconds)) elapsed") }
        if let tokens = goal.tokensUsed {
            if let budget = goal.tokenBudget {
                parts.append("\(tokens.formatted(.number.notation(.compactName))) of \(budget.formatted(.number.notation(.compactName))) tokens")
            } else {
                parts.append("\(tokens.formatted(.number.notation(.compactName))) tokens")
            }
        } else if let budget = goal.tokenBudget {
            parts.append("\(budget.formatted(.number.notation(.compactName))) token budget")
        }
        return parts.joined(separator: " · ")
    }

    private static func duration(_ seconds: Int) -> String {
        if seconds < 60 { return "\(seconds)s" }
        let minutes = seconds / 60
        if minutes < 60 { return "\(minutes)m" }
        return "\(minutes / 60)h \(minutes % 60)m"
    }
}
