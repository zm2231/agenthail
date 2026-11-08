import SwiftUI

struct NewSessionOptionsChip: View {
    @Binding var form: SessionLaunchForm
    @State private var open = false

    var body: some View {
        Button { open.toggle() } label: {
            Label(NewSessionLabels.options(form), systemImage: "slider.horizontal.3")
        }
        .buttonStyle(.plain)
        .font(.system(size: DesktopType.secondary))
        .foregroundStyle(form.outputSchemaProblem == nil ? DesktopPalette.text2 : DesktopPalette.failed)
        .padding(.horizontal, 8)
        .frame(height: 26)
        .overlay(Capsule().strokeBorder(DesktopPalette.line))
        .help("Name and launch options")
        .accessibilityIdentifier("new-session-options")
        .popover(isPresented: $open, arrowEdge: .bottom) {
            Form {
                TextField("Name", text: $form.alias, prompt: Text("Optional"))
                if form.setsLaunchOptions && form.agent == "codex" { codexFields }
                if form.setsLaunchOptions && form.agent == "claude" { claudeFields }
            }
            .formStyle(.grouped)
            .frame(width: 360)
        }
    }

    @ViewBuilder private var codexFields: some View {
        Picker("Approvals", selection: $form.approvalPolicy) {
            Text("Codex default").tag("")
            Text("Ask when needed").tag("on-request")
            Text("Ask for untrusted work").tag("untrusted")
            Text("Never ask").tag("never")
        }
        Picker("Mode", selection: $form.mode) {
            Text("Session default").tag("")
            Text("Plan").tag("plan")
            Text("Default").tag("default")
        }
        Picker("Service tier", selection: $form.serviceTier) {
            Text("Runtime default").tag("")
            Text("Default").tag("default")
            Text("Fast").tag("fast")
            Text("Flex").tag("flex")
        }
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Text("Output JSON schema")
            TextEditor(text: $form.outputSchema)
                .font(.system(size: DesktopType.secondary, design: .monospaced))
                .frame(height: 80)
                .accessibilityLabel("Output JSON schema")
            if let problem = form.outputSchemaProblem {
                Text(problem).foregroundStyle(DesktopPalette.failed)
            }
        }
    }

    @ViewBuilder private var claudeFields: some View {
        TextField("Agent", text: $form.claudeAgent, prompt: Text("Optional"))
        TextField("Worktree", text: $form.worktree, prompt: Text("Optional"))
        Picker("Permission mode", selection: $form.permissionMode) {
            Text("Claude default").tag("")
            Text("Plan").tag("plan")
            Text("Auto").tag("auto")
            Text("Manual").tag("manual")
            Text("Accept edits").tag("acceptEdits")
            Text("Don't ask").tag("dontAsk")
            Text("Bypass permissions").tag("bypassPermissions")
        }
    }
}
