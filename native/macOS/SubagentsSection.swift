import SwiftUI

struct SubagentsSection: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState

    var body: some View {
        let sessions = model.knownSessions
        let parent = session.subagent.flatMap { identity in sessions.first { $0.id == identity.parentId } }
        let children = SessionTree.newestFirst(sessions.filter { $0.subagent?.parentId == session.id })
        let observed = pane.detail?.session.id == session.id ? pane.detail?.claudeSubagents ?? [] : []
        if parent != nil || !children.isEmpty || !observed.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                SidebarCaption("Subagents").padding(.horizontal, -8)
                if let parent {
                    Button {
                        pane.select(parent.id)
                    } label: {
                        Label("Spawned by \(SessionFamilies.title(parent, in: sessions))", systemImage: "arrow.turn.left.up")
                            .lineLimit(1)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accent)
                }
                ForEach(children) { child in
                    Button {
                        pane.select(child.id)
                    } label: {
                        HStack(spacing: 8) {
                            StatusIndicator(session: child, needsYou: false)
                            Text(SessionFamilies.label(child)).lineLimit(1)
                            Spacer(minLength: 4)
                            Text(relativeAge(child.lastActive))
                                .font(.system(size: 11))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityValue(child.isWorking ? "Working" : "Idle")
                }
                ForEach(observed) { link in
                    ClaudeSubagentRow(link: link)
                }
            }
        }
    }
}

private struct ClaudeSubagentRow: View {
    let link: ClaudeSubagentLink

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 8) {
                Circle()
                    .fill(link.working == true ? DesktopPalette.work : DesktopPalette.muted)
                    .frame(width: 7, height: 7)
                    .frame(width: 12, height: 12)
                    .accessibilityHidden(true)
                Text(link.agentType.flatMap { $0.isEmpty ? nil : $0 } ?? link.agentId)
                    .lineLimit(1)
                Spacer(minLength: 4)
                if let lastActive = link.lastActive {
                    Text(relativeAge(lastActive))
                        .font(.system(size: 11))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            if let description = link.description, !description.isEmpty {
                Text(description)
                    .font(.system(size: 11.5))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(2)
                    .padding(.leading, 20)
            }
            Button {
                NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: link.transcriptPath)])
            } label: {
                Text((link.transcriptPath as NSString).abbreviatingWithTildeInPath)
                    .font(.system(size: 11, design: .monospaced))
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .padding(.leading, 20)
            .help("Show transcript in Finder")
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(link.working == true ? "Working" : "Idle")
    }
}
