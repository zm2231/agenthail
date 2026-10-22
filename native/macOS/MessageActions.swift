import AppKit
import SwiftUI

struct MessageActions: View {
    let text: String
    let revealed: Bool
    var onEdit: (() -> Void)? = nil
    @State private var copied = false
    @State private var hovering = false
    @FocusState private var focus: Action?

    private enum Action { case copy, edit }

    var body: some View {
        let shown = revealed || hovering || copied || focus != nil
        HStack(spacing: 2) {
            button(.copy, copied ? "Copied" : "Copy", symbol: copied ? "checkmark" : "doc.on.doc", action: copy)
            if let onEdit {
                button(.edit, "Edit", symbol: "pencil", action: onEdit)
            }
        }
        .foregroundStyle(shown ? DesktopPalette.text2 : Color.clear)
        .animation(.easeOut(duration: 0.12), value: shown)
        .onHover { hovering = $0 }
    }

    private func button(_ id: Action, _ label: String, symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: DesktopType.meta))
                .frame(width: 24, height: MessageActions.height)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .focused($focus, equals: id)
        .help(label)
        .accessibilityLabel(label)
    }

    static let height: CGFloat = 20

    private func copy() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        copied = true
        Task {
            try? await Task.sleep(for: .seconds(1.5))
            copied = false
        }
    }
}

struct MessageEditor: View {
    @State private var text: String
    let onCancel: () -> Void
    let onSend: (String) -> Void
    @FocusState private var focused: Bool

    init(text: String, onCancel: @escaping () -> Void, onSend: @escaping (String) -> Void) {
        _text = State(initialValue: text)
        self.onCancel = onCancel
        self.onSend = onSend
    }

    private var trimmed: String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        VStack(alignment: .trailing, spacing: DesktopSpace.s) {
            TextField("Edit message", text: $text, axis: .vertical)
                .textFieldStyle(.plain)
                .font(.system(size: DesktopType.prose))
                .lineSpacing(DesktopType.proseLineSpacing)
                .lineLimit(1...16)
                .focused($focused)
                .onSubmit(send)
                .onKeyPress(.escape) {
                    onCancel()
                    return .handled
                }
                .padding(.horizontal, 13)
                .padding(.vertical, 9)
                .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.sheet))
                .overlay(RoundedRectangle(cornerRadius: DesktopRadius.sheet).strokeBorder(DesktopPalette.accent))
            HStack(spacing: DesktopSpace.s) {
                Text("Sends as a new message")
                    .font(.system(size: DesktopType.caption))
                    .foregroundStyle(DesktopPalette.text2)
                Button("Cancel", action: onCancel)
                    .controlSize(.small)
                Button("Send", action: send)
                    .controlSize(.small)
                    .buttonStyle(.borderedProminent)
                    .tint(DesktopPalette.accent)
                    .disabled(trimmed.isEmpty)
            }
        }
        .onAppear { focused = true }
    }

    private func send() {
        guard !trimmed.isEmpty else { return }
        onSend(trimmed)
    }
}
