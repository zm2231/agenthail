import AppKit
import QuickLook
import SwiftUI

struct SentFileCard: View {
    @Environment(AgenthailModel.self) private var model
    @Environment(SessionPane.self) private var pane
    let item: TimelineItem
    @State private var preview: URL?
    @State private var loading = false
    @State private var failure: String?
    @State private var copied = false

    private var hostName: String? { pane.displayedSession?.host?.name ?? model.daemon?.host?.name }
    private var path: String { item.files?.first?.path ?? "" }

    var body: some View {
        let name = SentFileState.name(item)
        let reason = SentFileState.reason(item, hostName: hostName)
        Button { Task { await fetch { preview = $0 } } } label: {
            HStack(spacing: DesktopSpace.m) {
                Image(systemName: SentFileState.icon(item))
                    .font(.system(size: DesktopType.title))
                    .foregroundStyle(DesktopPalette.text2)
                    .frame(width: 28)
                VStack(alignment: .leading, spacing: 2) {
                    Text(name)
                        .font(.system(size: DesktopType.body, weight: .medium))
                        .foregroundStyle(DesktopPalette.text)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Text(copied ? "Path copied" : reason ?? failure ?? SentFileState.detail(item))
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                        .lineLimit(2)
                }
                Spacer(minLength: 0)
                if loading { ProgressView().controlSize(.small) }
            }
            .padding(.horizontal, DesktopSpace.m)
            .padding(.vertical, DesktopSpace.s)
            .frame(maxWidth: 420, alignment: .leading)
            .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(reason != nil || loading)
        .help(reason ?? "Preview \(name)")
        .accessibilityLabel(reason == nil ? "Preview \(name)" : "\(name), \(reason ?? "")")
        .accessibilityIdentifier("sent-file-" + item.id)
        .contextMenu {
            if reason == nil {
                Button("Quick Look") { Task { await fetch { preview = $0 } } }
                Button("Open With Default App") { Task { await fetch { NSWorkspace.shared.open($0) } } }
                Button("Show in Finder") { Task { await fetch { NSWorkspace.shared.activateFileViewerSelecting([$0]) } } }
            }
            if !path.isEmpty { Button("Copy Path", action: copyPath) }
        }
        .overlay(alignment: .bottomTrailing) {
            if reason != nil && !path.isEmpty {
                Button("Copy Path", action: copyPath)
                    .buttonStyle(.link)
                    .font(.system(size: DesktopType.meta))
                    .padding(DesktopSpace.s)
            }
        }
        .quickLookPreview($preview)
        .task(id: copied) {
            guard copied else { return }
            try? await Task.sleep(for: .seconds(2))
            copied = false
        }
    }

    private func copyPath() {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(path, forType: .string)
        copied = true
    }

    private func fetch(_ then: (URL) -> Void) async {
        guard let attachment = item.attachment, let sessionID = pane.displayedSession?.id, let api = model.api else { return }
        loading = true
        defer { loading = false }
        do {
            let url = try await api.downloadSentFile(sessionID: sessionID, attachment: attachment, name: SentFileState.name(item))
            failure = nil
            then(url)
        } catch {
            if !error.isCancellation { failure = error.localizedDescription }
        }
    }
}
