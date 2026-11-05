import SwiftUI

enum ConversationRoute {
    private static let projectPrefix = "\u{0}project:"

    static func project(_ id: String) -> String { projectPrefix + id }

    static func projectID(_ route: String) -> String? {
        route.hasPrefix(projectPrefix) ? String(route.dropFirst(projectPrefix.count)) : nil
    }
}

private extension View {
    @ViewBuilder
    func prominent(_ primary: Bool) -> some View {
        if primary { buttonStyle(.borderedProminent) } else { buttonStyle(.bordered) }
    }
}

struct ProjectScreen: View {
    @ObservedObject var model: AgenthailIOSModel
    let projectID: String
    let openSession: (String) -> Void
    @State private var sheet: ProjectSheet?
    @State private var confirmation: ProjectConfirmation?
    @State private var place: NewSessionPlace?
    @State private var showingEarlier = false

    private var project: ListedProject? { model.projects[projectID] }

    var body: some View {
        Group {
            if let project {
                content(project, ProjectContents.build(project, sessions: model.inDaemonOrder(model.snapshot?.sessions ?? [])))
            } else {
                ContentUnavailableView("Project not found", systemImage: "folder", description: Text("It may have been removed or its computer is not paired."))
            }
        }
        .phoneBackdrop(.list)
        .sheet(item: $place) { place in NewSessionSheet(model: model, place: place) }
        .sheet(item: $sheet) { sheet in IOSProjectSheetView(model: model, sheet: sheet) }
        .alert(confirmation?.message ?? "", isPresented: Binding(get: { confirmation != nil }, set: { if !$0 { confirmation = nil } }), presenting: confirmation) { confirmation in
            Button(confirmation.action, role: .destructive) { Task { model.settle(await confirmation.run()) } }
            Button("Cancel", role: .cancel) {}
        }
        .refreshable { await model.refresh(fresh: true) }
    }

    private func content(_ project: ListedProject, _ contents: ProjectContents) -> some View {
        let computers = Set(project.state.checkouts.map(\.host.id)).count
        return List {
            Section("Lead") { leadCard(project, contents) }
            Section {
                if contents.now.count == 0 {
                    Text("Nothing in the last day").foregroundStyle(PhonePalette.muted)
                }
                ForEach(contents.now.groups) { group in
                    if contents.now.showsGroups {
                        Text(group.title)
                            .font(.footnote.weight(.semibold)).foregroundStyle(PhonePalette.muted)
                            .lineLimit(1).truncationMode(.middle)
                            .listRowSeparator(.hidden, edges: .bottom)
                            .accessibilityAddTraits(.isHeader)
                            .accessibilityIdentifier("project-now-group-" + group.id)
                    }
                    ForEach(group.sessions) { row($0) }
                }
            } header: {
                HStack {
                    Text(contents.now.count == 0 ? "Now" : "Now · \(contents.now.count)")
                    Spacer()
                    if !showingEarlier { Button("Earlier") { showingEarlier = true }.textCase(nil).accessibilityIdentifier("project-earlier") }
                }
            }
            if showingEarlier {
                Section("Earlier") {
                    ForEach(contents.earlier) { row($0) }
                    if model.hasEarlier(project) {
                        ProgressView().frame(maxWidth: .infinity).listRowBackground(Color.clear)
                            .task(id: contents.earlier.count) { await model.listEarlier(project) }
                    } else if contents.earlier.isEmpty {
                        Text("No earlier sessions").foregroundStyle(PhonePalette.muted)
                    }
                }
            }
            Section {
                HStack(spacing: 10) {
                    Button("New session") { newSession(project) }.buttonStyle(.borderedProminent)
                    Button(contents.lead == nil ? "New lead" : "Change lead") { sheet = .lead(project.state) }.buttonStyle(.bordered)
                }
                .frame(maxWidth: .infinity)
                .listRowBackground(Color.clear)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(project.state.displayName)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                VStack(spacing: 0) {
                    Text(project.state.displayName).font(.headline)
                    if computers > 1 { Text("\(computers) computers").font(.caption).foregroundStyle(PhonePalette.muted) }
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    IOSProjectMenuItems(model: model, project: project, sheet: $sheet, confirmation: $confirmation, place: $place)
                } label: {
                    Label("Project settings", systemImage: "ellipsis.circle")
                }
            }
        }
    }

    @ViewBuilder
    private func leadCard(_ project: ListedProject, _ contents: ProjectContents) -> some View {
        if let lead = contents.lead {
            VStack(alignment: .leading, spacing: 10) {
                if let session = contents.leadSession {
                    Button { openSession(session.id) } label: {
                        HStack(alignment: .top, spacing: 12) {
                            SessionAvatar(surface: session.surface, ring: SessionRing.kind(session, needsYou: model.sessionAttention[session.id] != nil))
                            IOSSessionRow(session: session, title: "@" + lead.name, showsHost: true, showsTime: false)
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("project-lead")
                    TimelineView(.everyMinute) { context in
                        Text(SessionRowTime.status(session, now: context.date))
                            .font(.subheadline).foregroundStyle(PhonePalette.muted)
                            .accessibilityIdentifier("project-lead-status")
                    }
                    HStack(spacing: 10) {
                        ForEach(Array(ProjectContents.leadActions(session).enumerated()), id: \.offset) { index, action in
                            switch action {
                            case .resume(let enabled, let lifecycle):
                                Button("Resume") { resume(session, lifecycle: lifecycle) }
                                    .disabled(!enabled)
                                    .accessibilityIdentifier("project-lead-resume")
                                    .prominent(index == 0)
                            case .open:
                                Button("Open") { openSession(session.id) }.prominent(index == 0)
                            case .message:
                                Button("Message") { openSession(session.id) }.prominent(index == 0)
                            }
                        }
                    }
                    if !session.isWorking && session.isUnavailable {
                        Text("Resume needs \(session.host?.name ?? "its computer") to be reachable.")
                            .font(.caption).foregroundStyle(PhonePalette.muted)
                    }
                } else {
                    Text("@" + lead.name).font(.headline)
                    Text("\(SessionStyle.agentName(lead.surface)) · \(ProjectContents.leadNote(lead))").font(.subheadline).foregroundStyle(PhonePalette.muted)
                }
                if let replaced = lead.replaced {
                    TimelineView(.everyMinute) { context in
                        Text(replaced.detail(now: context.date))
                            .font(.caption).foregroundStyle(PhonePalette.muted)
                            .accessibilityIdentifier("project-lead-replaced")
                    }
                }
            }
            .padding(.vertical, 4)
        } else {
            VStack(alignment: .leading, spacing: 10) {
                Text("No lead yet").font(.headline)
                Text("A lead keeps this project's work in one conversation you can come back to.").font(.subheadline).foregroundStyle(PhonePalette.muted)
                Button("Start a lead") { sheet = .lead(project.state) }.buttonStyle(.borderedProminent).accessibilityIdentifier("project-start-lead")
            }
            .padding(.vertical, 4)
        }
    }

    private func row(_ session: SessionState) -> some View {
        Button { openSession(session.id) } label: {
            HStack(alignment: .top, spacing: 12) {
                SessionAvatar(surface: session.surface, ring: SessionRing.kind(session, needsYou: model.sessionAttention[session.id] != nil))
                IOSSessionRow(session: session, showsHost: model.showsHostNames)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier("project-session-" + session.id)
    }

    private func resume(_ session: SessionState, lifecycle: Bool) {
        if lifecycle { Task { await model.resume(session.id) } }
        openSession(session.id)
    }

    private func newSession(_ project: ListedProject) {
        guard let checkout = project.state.checkouts.first(where: { $0.archived != true }) else { return }
        place = NewSessionPlace(host: model.creationHost(for: checkout, of: project), folder: checkout.rootPath)
    }
}
