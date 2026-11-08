import SwiftUI

struct TurnOptionsChip: View {
    @ObservedObject var draft: ComposerDraft
    @State private var open = false

    static func label(serviceTier: String, outputSchema: String) -> String {
        let schema = outputSchema.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : "Schema"
        let parts = [serviceTier.isEmpty ? nil : serviceTier.capitalized, schema].compactMap { $0 }
        return parts.isEmpty ? "Turn options" : parts.joined(separator: " · ")
    }

    var body: some View {
        Button { open.toggle() } label: {
            HStack(spacing: 3) {
                Text(Self.label(serviceTier: draft.serviceTier, outputSchema: draft.outputSchema))
                    .lineLimit(1)
                Image(systemName: "chevron.up.chevron.down")
                    .imageScale(.small)
            }
            .font(.system(size: DesktopType.meta, weight: .medium))
            .foregroundStyle(draft.turnOptionsProblem == nil ? DesktopPalette.text2 : DesktopPalette.failed)
            .fixedSize()
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help("Service tier and output schema for messages you send to this session")
        .accessibilityLabel("Turn options, \(Self.label(serviceTier: draft.serviceTier, outputSchema: draft.outputSchema))")
        .accessibilityIdentifier("composer-turn-options")
        .popover(isPresented: $open, arrowEdge: .top) {
            Form {
                Picker("Service tier", selection: $draft.serviceTier) {
                    Text("Session default").tag("")
                    Text("Default").tag("default")
                    Text("Fast").tag("fast")
                    Text("Flex").tag("flex")
                }
                VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                    Text("Output JSON schema")
                    TextEditor(text: $draft.outputSchema)
                        .font(.system(size: DesktopType.secondary, design: .monospaced))
                        .frame(height: 90)
                        .accessibilityLabel("Output JSON schema")
                    if let problem = draft.turnOptionsProblem {
                        Text(problem).foregroundStyle(DesktopPalette.failed)
                    }
                }
                Text("Applies to each message you send here until you clear it. Steer can't carry these, so a message with them waits for the turn to end.")
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
            }
            .formStyle(.grouped)
            .frame(width: 360)
        }
    }
}
