import AppKit
import SwiftUI

extension ComposerAttachments {
    // Nil means nothing on the pasteboard is an attachment, so ⌘V stays a text paste.
    static func pasted(from pasteboard: NSPasteboard) -> PastedAttachments? {
        var pasted = PastedAttachments()
        for item in pasteboard.pasteboardItems ?? [] {
            guard let type = pastedType(item.types.map(\.rawValue), text: item.string(forType: .string)) else { continue }
            pasted.add(item.data(forType: NSPasteboard.PasteboardType(type)), type: type)
        }
        return pasted.isEmpty ? nil : pasted
    }
}

// The focused text field handles ⌘V before onPasteCommand would see it, so
// the composer watches the keys itself and lets text pastes through.
struct ComposerPasteMonitor: NSViewRepresentable {
    let isActive: Bool
    let paste: (PastedAttachments) -> Void

    func makeNSView(context _: Context) -> MonitorView { MonitorView() }

    func updateNSView(_ view: MonitorView, context _: Context) {
        view.isActive = isActive
        view.paste = paste
    }

    static func dismantleNSView(_ view: MonitorView, coordinator _: ()) { view.stop() }

    final class MonitorView: NSView {
        var isActive = false
        var paste: (PastedAttachments) -> Void = { _ in }
        private var monitor: Any?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            stop()
            guard window != nil else { return }
            monitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
                MainActor.assumeIsolated { self?.pastes(event) == true } ? nil : event
            }
        }

        func stop() {
            if let monitor { NSEvent.removeMonitor(monitor) }
            monitor = nil
        }

        // ⌃V pastes too, as in Claude Code; without an image it pastes the
        // text that ⌘V would, rather than the text field's own ⌃V binding.
        private func pastes(_ event: NSEvent) -> Bool {
            let modifiers = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
            guard isActive, event.window === window, modifiers == .command || modifiers == .control,
                  event.charactersIgnoringModifiers?.lowercased() == "v" else { return false }
            if let pasted = ComposerAttachments.pasted(from: .general) {
                paste(pasted)
                return true
            }
            guard modifiers == .control else { return false }
            return NSApp.sendAction(#selector(NSText.paste(_:)), to: nil, from: self)
        }
    }
}
