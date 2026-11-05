import SwiftUI

extension ProjectContents {
    static func leadSessionID(_ lead: ProjectState.Lead) -> String? {
        guard let sessionID = lead.sessionId, !sessionID.isEmpty else { return nil }
        guard let host = lead.host, host.state != "local", !sessionID.contains("::") else { return sessionID }
        return host.id + "::" + sessionID
    }

    static func build(_ project: ProjectState, sessions: [SessionState]) -> ProjectContents {
        let lead = project.leads.first
        return build(lead: lead, leadID: lead.flatMap(leadSessionID), projectID: project.id, sessions: sessions)
    }
}

extension View {
    @ViewBuilder
    func opensProject(_ projectID: String?, model: AgenthailModel, pane: SessionPane) -> some View {
        if let projectID, model.projects.contains(where: { $0.id == projectID }) {
            Button { pane.showProject(projectID) } label: { self.contentShape(Rectangle()) }
                .buttonStyle(.plain)
                .selectionMark(pane.shownProjectID == projectID, bar: true)
                .accessibilityHint("Opens the project")
                .accessibilityIdentifier("open-project-" + projectID)
        } else {
            self
        }
    }
}

private extension View {
    @ViewBuilder
    func prominent(_ primary: Bool) -> some View {
        if primary { buttonStyle(.borderedProminent).tint(DesktopPalette.accent) } else { buttonStyle(.bordered) }
    }
}

struct ProjectPane: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    let project: ProjectState
    @State private var showingEarlier = false

    var body: some View {
        let contents = ProjectContents.build(project, sessions: model.inDaemonOrder(model.knownSessions))
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.l) {
                header(contents)
                HStack(alignment: .top, spacing: DesktopSpace.l) {
                    VStack(alignment: .leading, spacing: DesktopSpace.l) {
                        if !contents.running.isEmpty { card("Running now · \(contents.running.count)") { rows(contents.running) } }
                        card("Recent · last 24 hours", trailing: showingEarlier ? nil : ("Show earlier", { showingEarlier = true })) {
                            if contents.recent.isEmpty { empty("Nothing in the last day") }
                            rows(contents.recent)
                        }
                        if showingEarlier {
                            card("Earlier") {
                                if contents.earlier.isEmpty && !model.hasEarlier(project.id) { empty("No earlier sessions") }
                                rows(contents.earlier)
                                if model.hasEarlier(project.id) {
                                    ProgressView()
                                        .controlSize(.small)
                                        .frame(maxWidth: .infinity)
                                        .task(id: model.pages.listings[model.earlierRequest(project.id)]?.rows.count ?? -1) { await model.listEarlier(project.id) }
                                }
                            }
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .topLeading)
                    VStack(alignment: .leading, spacing: DesktopSpace.l) {
                        card("Lead") { lead(contents) }
                        card("Folders") { folders }
                    }
                    .frame(width: 340)
                }
            }
            .padding(DesktopSpace.xl)
        }
        .scrimText()
        .backdropSurface(DesktopPalette.window)
    }

    private func header(_ contents: ProjectContents) -> some View {
        let computers = Set(project.checkouts.map(\.host.id)).count
        let count = contents.running.count + contents.recent.count + contents.earlier.count + (contents.leadSession == nil ? 0 : 1)
        return HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
            Text(project.displayName).font(.system(size: DesktopType.display, weight: .semibold)).foregroundStyle(DesktopPalette.text)
            Text("\(SessionTree.sessionCountLabel(count))\(computers > 1 ? " on \(computers) computers" : "")")
                .font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
            Spacer()
            Menu("Project settings") { ProjectMenuButtons(model: model, pane: pane, project: project) }
                .fixedSize()
            Button(contents.lead == nil ? "New lead…" : "Change lead…") { pane.projectSheet = .lead(project) }
            Button("New session") { newSession() }
                .prominent(true)
                .disabled(project.checkouts.allSatisfy { $0.archived == true })
        }
    }

    @ViewBuilder
    private func lead(_ contents: ProjectContents) -> some View {
        if let lead = contents.lead {
            VStack(alignment: .leading, spacing: DesktopSpace.s) {
                Text("@" + lead.name).font(.system(size: DesktopType.title, weight: .semibold)).foregroundStyle(DesktopPalette.text)
                if let session = contents.leadSession {
                    TimelineView(.everyMinute) { context in
                        Text([SessionRefinement.surfaceLabel(session.surface), session.remoteHostName ?? model.snapshot?.daemon.host?.name, SessionRowTime.status(session, now: context.date)].compactMap { $0 }.joined(separator: " · "))
                            .font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                            .accessibilityIdentifier("project-lead-status")
                    }
                    HStack(spacing: DesktopSpace.s) {
                        ForEach(Array(ProjectContents.leadActions(session).enumerated()), id: \.offset) { index, action in
                            switch action {
                            case .resume(let enabled, let lifecycle):
                                Button("Resume") { resume(session, lifecycle: lifecycle) }.disabled(!enabled).prominent(index == 0)
                            case .open:
                                Button("Open") { pane.select(session.id) }.prominent(index == 0)
                            case .message:
                                Button("Message…") { pane.select(session.id) }.prominent(index == 0)
                            }
                        }
                    }
                    if !session.isWorking && session.isUnavailable {
                        Text("Resume needs \(session.remoteHostName ?? "its computer") to be reachable.")
                            .font(.system(size: DesktopType.meta)).foregroundStyle(DesktopPalette.text2)
                    }
                } else {
                    Text("\(SessionRefinement.surfaceLabel(lead.surface)) · \(ProjectContents.leadNote(lead))")
                        .font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                }
                if let replaced = lead.replaced {
                    TimelineView(.everyMinute) { context in
                        Text(replaced.detail(now: context.date))
                            .font(.system(size: DesktopType.meta)).foregroundStyle(DesktopPalette.text2)
                            .accessibilityIdentifier("project-lead-replaced")
                    }
                }
            }
        } else {
            VStack(alignment: .leading, spacing: DesktopSpace.s) {
                Text("No lead yet").font(.system(size: DesktopType.title, weight: .semibold)).foregroundStyle(DesktopPalette.text)
                Text("A lead keeps this project's work in one conversation you can return to. Messages to it always land.")
                    .font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                Button("Start a lead") { pane.projectSheet = .lead(project) }.prominent(true)
            }
        }
    }

    private var folders: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            ForEach(project.checkouts, id: \.self) { checkout in
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.m) {
                    Text(checkout.host.state == "local" ? model.snapshot?.daemon.host?.name ?? checkout.host.name : checkout.host.name)
                        .font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                        .frame(width: 110, alignment: .leading)
                        .lineLimit(1)
                    Text(checkout.rootPath)
                        .font(.system(size: DesktopType.caption, design: .monospaced)).foregroundStyle(DesktopPalette.text)
                        .lineLimit(1).truncationMode(.middle)
                        .textSelection(.enabled)
                }
            }
        }
    }

    private func rows(_ sessions: [SessionState]) -> some View {
        ForEach(sessions) { session in
            Button { pane.select(session.id) } label: {
                SessionRowView(session: session, title: SessionFamilies.title(session, in: model.knownSessions), needsYou: model.sessionAttention[session.id] != nil)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("project-session-" + session.id)
        }
    }

    private func card<Content: View>(_ title: String, trailing: (String, () -> Void)? = nil, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            HStack {
                Text(title.uppercased()).font(.system(size: DesktopType.caption, weight: .semibold)).foregroundStyle(DesktopPalette.text2)
                Spacer()
                if let trailing {
                    Button(trailing.0, action: trailing.1).buttonStyle(.plain).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.accentText)
                }
            }
            content()
        }
        .padding(DesktopSpace.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: DesktopRadius.card))
    }

    private func empty(_ text: String) -> some View {
        Text(text).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2).padding(.vertical, DesktopSpace.xs)
    }

    private func resume(_ session: SessionState, lifecycle: Bool) {
        if lifecycle { model.perform(action: "session-lifecycle-resume", sessionID: session.id) }
        pane.select(session.id)
    }

    private func newSession() {
        guard let checkout = project.checkouts.first(where: { $0.archived != true }) else { return }
        pane.openNewSession(host: ProjectMenu.host(of: checkout), folder: checkout.rootPath)
    }
}
