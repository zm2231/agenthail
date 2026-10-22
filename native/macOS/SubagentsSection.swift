import SwiftUI

struct SubagentsSection: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState

    var body: some View {
        let sessions = model.knownSessions
        let parent = session.subagent.flatMap { identity in sessions.first { $0.id == identity.parentId } ?? model.fetchedSubagents.session(identity.parentId) }
        let children = SessionTree.newestFirst(model.subagents(of: session.id))
        let observed = pane.detail?.session.id == session.id ? pane.detail?.claudeSubagents ?? [] : []
        let loading = model.fetchedSubagents.loading(session)
        let error = model.fetchedSubagents.errors[session.id]
        if parent != nil || !children.isEmpty || !observed.isEmpty || loading || error != nil {
            VStack(alignment: .leading, spacing: 6) {
                SidebarCaption("Subagents").padding(.horizontal, -8)
                if let parent {
                    Button {
                        model.openSubagent(parent, in: pane)
                    } label: {
                        Label("Spawned by \(SessionFamilies.title(parent, in: sessions))", systemImage: "arrow.turn.left.up")
                            .lineLimit(1)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(DesktopPalette.accent)
                }
                ForEach(children) { child in
                    Button {
                        model.openSubagent(child, in: pane)
                    } label: {
                        HStack(spacing: DesktopSpace.s) {
                            StatusIndicator(session: child, needsYou: false)
                            Text(SessionFamilies.label(child)).lineLimit(1)
                            Spacer(minLength: 4)
                            Text(relativeAge(child.lastActive))
                                .font(.system(size: DesktopType.caption))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityValue(child.isWorking ? "Working" : "Idle")
                }
                if loading || error != nil {
                    Text(error ?? "Loading subagents")
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
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
            HStack(spacing: DesktopSpace.s) {
                Circle()
                    .fill(link.working == true ? DesktopPalette.working : DesktopPalette.muted)
                    .frame(width: 7, height: 7)
                    .frame(width: 12, height: 12)
                    .accessibilityHidden(true)
                Text(link.agentType.flatMap { $0.isEmpty ? nil : $0 } ?? link.agentId)
                    .lineLimit(1)
                Spacer(minLength: 4)
                if let lastActive = link.lastActive {
                    Text(relativeAge(lastActive))
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            if let description = link.description, !description.isEmpty {
                Text(description)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(2)
                    .padding(.leading, DesktopSpace.xl)
            }
            Button {
                NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: link.transcriptPath)])
            } label: {
                Text((link.transcriptPath as NSString).abbreviatingWithTildeInPath)
                    .font(.system(size: DesktopType.caption, design: .monospaced))
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            .buttonStyle(.plain)
            .foregroundStyle(DesktopPalette.text2)
            .padding(.leading, DesktopSpace.xl)
            .help("Show transcript in Finder")
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(link.working == true ? "Working" : "Idle")
    }
}
