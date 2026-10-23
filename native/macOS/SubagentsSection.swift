import SwiftUI

struct SubagentsSection: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let session: SessionState

    var body: some View {
        let sessions = model.knownSessions
        let parent = session.subagent.flatMap { identity in sessions.first { $0.id == identity.parentId } ?? model.fetchedSubagents.session(identity.parentId) }
        let children = SessionTree.newestFirst(model.subagents(of: session.id))
        let current = pane.detail?.session.id == session.id ? pane.detail : nil
        let observed = ClaudeSubagentList(current?.claudeSubagents ?? [], total: current?.claudeSubagentsTotal, expanded: pane.showsAllClaudeSubagents)
        let loading = model.fetchedSubagents.loading(session)
        let error = model.fetchedSubagents.errors[session.id]
        if parent != nil || !children.isEmpty || observed.total > 0 || loading || error != nil {
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
                if observed.total > 0 {
                    Text(observed.summary)
                        .font(.system(size: DesktopType.meta))
                        .foregroundStyle(DesktopPalette.text2)
                }
                ForEach(observed.shown) { link in
                    ClaudeSubagentRow(link: link)
                }
                if observed.earlier > 0 {
                    Button(observed.earlierLabel) { pane.showAllClaudeSubagents() }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.accent)
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
                Text(link.title)
                    .lineLimit(2)
                Spacer(minLength: 4)
                if let lastActive = link.lastActive {
                    Text(relativeAge(lastActive))
                        .font(.system(size: DesktopType.caption))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            if let kind = link.kindLabel {
                Text(kind)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.leading, DesktopSpace.xl)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(link.working == true ? "Working" : "Idle")
    }
}
