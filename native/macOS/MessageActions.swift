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
        .foregroundStyle(DesktopPalette.text2.when(shown))
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
