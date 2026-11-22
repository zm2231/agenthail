import SwiftUI

struct ConversationListView: View {
    @Bindable var model: AgenthailIOSModel
    var selectedID: String? = nil
    let openSession: (String) -> Void
    @State private var showingNewSession = false
    @State private var search = ""
    @State private var expandedProjects: Set<String> = []
    @State private var familyChoices: [String: Bool] = [:]
    @State private var projectSheet: ProjectSheet?
    @State private var projectConfirmation: ProjectConfirmation?
    @State private var newSessionPlace: NewSessionPlace?
    @State private var messagingProject: ListedProject?
    @StateObject private var usage = UsageFeed()
    @ScaledMetric(relativeTo: .body) private var titleInset: CGFloat = 53

    private var scope: SessionFilter { model.sessionListing.scope }

    private func projectsWithoutSessions(_ tree: SessionTree) -> [ListedProject] {
        guard (scope == .recent || scope == .all) && search.isEmpty && !model.sessionListing.refinement.isActive else { return [] }
        let listed = Set(tree.projects.compactMap(\.identity?.id))
        return model.projectOrder.filter { !listed.contains($0) }.compactMap { model.projects[$0] }.filter { $0.state.archived != true }
    }

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("ConversationListView")
#endif
        let list = model.sessionListing.sessionList(search: search)
        let tree = list.tree
        let families = tree.projects.flatMap { $0.checkouts.flatMap(\.families) }
        List {
            if search.isEmpty {
                PhoneProjectTiles(model: model, open: openSession)
                    .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 0, trailing: 0))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Color.clear)
            }
            SessionScopeChips(scope: Bindable(model.sessionListing).scope, counts: list.counts)
                .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 4, trailing: 0))
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            if model.sessionListing.refinement.isActive {
                IOSSessionFilterChips(model: model)
                    .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 4, trailing: 0))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Color.clear)
            }
            UsageMetersRow(feed: usage, model: model, open: openSession)
            if let error = model.connectionError {
                Label(error, systemImage: "wifi.exclamationmark").font(.footnote).foregroundStyle(PhonePalette.muted)
                    .listRowBackground(GlassRowBackground(selected: false))
            } else if model.reconnecting {
                Label("Reconnecting to your computer", systemImage: "desktopcomputer").font(.footnote).foregroundStyle(PhonePalette.muted)
                    .listRowBackground(GlassRowBackground(selected: false))
            }
            if !tree.needsYou.isEmpty {
                Section {
                    ForEach(tree.needsYou) { session in
                        row(.value(session), id: session.id, reason: tree.attention[session.id]?.reason, identifierPrefix: "needs-you-")
                    }
                } header: {
                    Label {
                        Text("Needs you")
                    } icon: {
                        Image(systemName: "exclamationmark.bubble.fill").foregroundStyle(PhonePalette.attention)
                    }
                    .font(.subheadline.weight(.semibold)).foregroundStyle(.primary).textCase(nil).glassLabel()
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("Needs you")
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("needs-you-header")
                }
            }
            ForEach(tree.projects) { project in
                IOSProjectSection(model: model, project: project, selectedID: selectedID, expandedProjects: $expandedProjects,
                                  sheet: $projectSheet, confirmation: $projectConfirmation, place: $newSessionPlace, messaging: $messagingProject,
                                  openSession: openSession) { family in familyRows(family) }
            }
            let empty = projectsWithoutSessions(tree)
            if !empty.isEmpty {
                Section("Projects without sessions") {
                    ForEach(empty) { listed in
                        let computers = HostNames.ordered(listed.state.checkouts.map(\.host))
                        Menu {
                            IOSProjectMenuItems(model: model, project: listed, sheet: $projectSheet, confirmation: $projectConfirmation, place: $newSessionPlace, messaging: $messagingProject)
                        } label: {
                            Label {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(listed.state.displayName).foregroundStyle(.primary)
                                    if computers.count > 1 {
                                        Text(computers.joined(separator: ", ")).font(.caption).foregroundStyle(PhonePalette.muted)
                                    }
                                }
                            } icon: {
                                Image(systemName: "folder")
                            }
                        }
                        .accessibilityIdentifier("empty-project-" + listed.id)
                    }
                }
            }
            let listingMore = search.isEmpty && model.sessionListing.canListMore
            if let error = model.sessionListing.listingError, model.sessionListing.listRequest != nil {
                Button { Task { await model.sessionListing.listMore() } } label: {
                    Label(error, systemImage: "arrow.clockwise").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                .accessibilityIdentifier("list-more-retry")
            } else if listingMore {
                ProgressView().frame(maxWidth: .infinity).listRowBackground(Color.clear)
                    .task(id: "\(String(describing: model.sessionListing.listRequest))|\(model.sessionCount)") { await model.sessionListing.listMore() }
                    .accessibilityIdentifier("list-more")
            }
            if families.isEmpty && model.sessionListing.searchPending(search) {
                Text("Searching")
                    .font(.footnote)
                    .foregroundStyle(PhonePalette.muted)
                    .listRowBackground(Color.clear)
            } else if families.isEmpty && model.sessionListing.refinement.isActive && !model.searching && !listingMore {
                ContentUnavailableView {
                    Label("No sessions match these filters.", systemImage: "line.3.horizontal.decrease.circle")
                } actions: {
                    Button("Clear Filters") { model.sessionListing.refinement = SessionRefinement() }
                        .buttonStyle(.bordered)
                        .accessibilityIdentifier("clear-filters")
                }
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            } else if families.isEmpty && empty.isEmpty && !model.searching && !listingMore {
                ContentUnavailableView {
                    Label(scope == .running ? "No agents working" : scope == .archived ? "Nothing archived" : "No sessions here", systemImage: "bubble.left.and.bubble.right")
                } description: {
                    Text(search.isEmpty ? "Start a session, or browse All to continue earlier work." : "Try another title or search All sessions.")
                } actions: {
                    Button("New session", systemImage: "plus") { showingNewSession = true }
                }
                .listRowSeparator(.hidden)
                .listRowBackground(Color.clear)
            }
            if search.count >= 3 {
                Section("From saved history") {
                    if model.searching { ProgressView("Searching") }
                    if let error = model.searchError { Text(error).font(.footnote).foregroundStyle(PhonePalette.muted) }
                    ForEach(model.searchResults.filter { result in !families.contains(where: { $0.contains(result.id) }) }) { result in
                        row(.value(result.session), id: result.id, snippet: result.snippet)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .listSectionSpacing(14)
        .accessibilityIdentifier("session-list")
        .holdsOrder(model)
        .phoneBackdrop(.list)
        .navigationTitle("Sessions")
        // A title that collapses with scroll loops the pop's layout when the list overflows.
        .navigationBarTitleDisplayMode(.inline)
        .searchable(text: $search, prompt: "Search sessions")
        .task(id: search) { await model.searchSessions(search) }
        .task(id: "\(scope.rawValue)\u{0}\(String(describing: model.sessionListing.refinement))\u{0}\(search)") {
            do { try await Task.sleep(for: .milliseconds(250)) } catch { return }
            await model.sessionListing.searchScope(search)
        }
        .task(id: model.usageReaders.map(ObjectIdentifier.init)) { await usage.run(model.usageReaders) }
        .toolbar {
            if model.isPaired {
                ToolbarItem(placement: .topBarLeading) { VoiceToolbarButton(model: model) }
            }
            ToolbarItem(placement: .topBarTrailing) { IOSSessionFilterMenu(model: model, surfaces: list.surfaces) }
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button("New session", systemImage: "square.and.pencil") {
                        model.creationError = nil
                        showingNewSession = true
                    }.accessibilityIdentifier("new-session")
                    Button("Add project", systemImage: "folder.badge.plus") { projectSheet = .add(host: nil, folder: "") }
                        .accessibilityIdentifier("add-project")
                } label: {
                    Image(systemName: "plus")
                } primaryAction: {
                    model.creationError = nil
                    showingNewSession = true
                }
                .accessibilityLabel("New")
                .accessibilityIdentifier("new-menu")
            }
        }
        .sheet(isPresented: $showingNewSession) { NewSessionSheet(model: model) }
        .sheet(item: $newSessionPlace) { place in NewSessionSheet(model: model, place: place) }
        .sheet(item: $projectSheet) { sheet in IOSProjectSheetView(model: model, sheet: sheet) }
        .sheet(item: $messagingProject) { project in IOSProjectMessageSheet(model: model, project: project) }
        .alert(projectConfirmation?.message ?? "", isPresented: Binding(get: { projectConfirmation != nil }, set: { if !$0 { projectConfirmation = nil } }), presenting: projectConfirmation) { confirmation in
            Button(confirmation.action, role: .destructive) { Task { model.settle(await confirmation.run()) } }
            Button("Cancel", role: .cancel) {}
        }
        .modifier(IOSRowActionHost(model: model))
        .refreshable { await model.refresh(fresh: true) }
    }

    @ViewBuilder
    private func familyRows(_ family: SessionFamily) -> some View {
        let expanded = family.isExpanded(chosen: familyChoices, selectedID: selectedID)
        row(.root(family), id: family.id)
        if !family.members.isEmpty {
            Button { familyChoices[family.id] = !expanded } label: {
                Label(expanded ? "Hide \(SessionFamilies.membersNoun(family))" : "Show \(SessionFamilies.membersCount(family))",
                      systemImage: expanded ? "chevron.down" : "chevron.right")
                    .font(.footnote)
                    .foregroundStyle(PhonePalette.muted)
                    .frame(maxWidth: .infinity, minHeight: 32, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .padding(.leading, titleInset)
            .listRowBackground(GlassRowBackground(selected: false))
            .accessibilityIdentifier("subagents-" + family.id)
        }
        if expanded {
            memberRows(family.crew)
            if !family.taskRuns.isEmpty {
                let tasksExpanded = family.tasksExpanded(chosen: familyChoices, selectedID: selectedID)
                Button { familyChoices[family.tasksID] = !tasksExpanded } label: {
                    Label(SessionFamilies.tasksToggle(family, expanded: tasksExpanded), systemImage: tasksExpanded ? "chevron.down" : "chevron.right")
                        .font(.footnote)
                        .foregroundStyle(PhonePalette.muted)
                        .frame(maxWidth: .infinity, minHeight: 32, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.leading, titleInset + 16)
                .listRowBackground(GlassRowBackground(selected: false))
                .accessibilityIdentifier("tasks-" + family.id)
                if tasksExpanded {
                    memberRows(family.taskRuns)
                }
            }
        }
    }

    private func memberRows(_ members: [SessionFamily.Member]) -> some View {
        ForEach(members) { member in
            row(.member(member), id: member.id)
                .padding(.leading, CGFloat(member.depth) * 16)
        }
    }

    private func row(_ source: SessionListRow.Source, id: String, snippet: String? = nil, reason: String? = nil, identifierPrefix: String = "session-") -> SessionListRow {
        SessionListRow(model: model, source: source, needsYou: model.sessionListing.sessionTree(search: search).attention[id] != nil, selected: selectedID == id,
                       snippet: snippet, reason: reason, identifierPrefix: identifierPrefix, open: openSession)
    }
}

struct IOSSessionRow: View {
    let session: SessionState
    var title: String? = nil
    var family: SessionFamily? = nil
    var showsHost = true
    var showsTime = true
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    private var displayTitle: String {
        title ?? SessionStyle.title(session)
    }

    @ViewBuilder
    private func meta(spaced: Bool) -> some View {
        let kind = SessionDisplayKind.of(session)
        Text(SessionStyle.agentName(session.surface)).font(.caption.weight(.semibold)).foregroundStyle(PhonePalette.muted)
            .lineLimit(1).fixedSize()
        if let host = session.shortRemoteHostName, showsHost || kind == .unavailable {
            IOSHostBadge(name: host, unavailable: kind == .unavailable)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("On \(host)")
        }
        if let move = session.move {
            HStack(spacing: 4) {
                Image(systemName: "arrow.right.circle")
                Text(move.label)
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.muted)
            .lineLimit(1)
        } else if kind == .waiting {
            HStack(spacing: 4) {
                Image(systemName: "questionmark.bubble")
                Text(session.waitingPlace?.label ?? "Waiting on you")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.attention)
            .lineLimit(1)
            .fixedSize()
        } else if kind == .unavailable {
            Text("Unavailable")
                .font(.caption)
                .foregroundStyle(PhonePalette.failed)
                .lineLimit(1)
                .fixedSize()
        } else if kind == .limited, let limit = session.limitLabel {
            HStack(spacing: 4) {
                Image(systemName: "gauge.with.dots.needle.100percent")
                Text(limit)
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.muted)
            .lineLimit(1)
            .fixedSize()
        } else if kind == .stopped {
            HStack(spacing: 4) {
                Image(systemName: kind.symbol)
                Text(kind.label)
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.failed)
            .lineLimit(1)
            .fixedSize()
        } else if kind == .working {
            HStack(spacing: 4) {
                Image(systemName: "waveform")
                Text("Working")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.working)
            .lineLimit(1)
            .fixedSize()
        } else if kind == .finishing {
            HStack(spacing: 4) {
                Image(systemName: "hourglass")
                Text("Background work")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.working)
            .lineLimit(1)
            .fixedSize()
            .accessibilityLabel("Waiting on background work")
        } else if SessionDisplayKind.idleLead(session) {
            HStack(spacing: 4) {
                Image(systemName: "flag")
                Text("Lead")
            }
            .font(.caption)
            .foregroundStyle(PhonePalette.muted)
            .lineLimit(1)
            .fixedSize()
            .accessibilityIdentifier("session-lead-" + session.id)
        }
        if let plan = session.livePlan {
            HStack(spacing: 4) {
                Image(systemName: "checklist")
                Text(plan.badge)
            }
            .font(.caption.monospacedDigit())
            .foregroundStyle(PhonePalette.muted)
            .lineLimit(1)
            .fixedSize()
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(plan.detail)
        }
        if spaced { Spacer(minLength: 0) }
    }

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("IOSSessionRow")
#endif
        let kind = SessionDisplayKind.of(session)
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(displayTitle).font(.body.weight(.semibold)).lineLimit(dynamicTypeSize.isAccessibilitySize ? nil : 2).foregroundStyle(.primary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if session.isPinned {
                    Image(systemName: "pin.fill").font(.caption).foregroundStyle(PhonePalette.muted)
                        .accessibilityLabel("Pinned")
                        .accessibilityIdentifier("session-pinned-" + session.id)
                }
                if !showsTime {
                    EmptyView()
                } else if kind == .working {
                    PhoneWorkingTime(session: session)
                } else if let value = session.lastActive, let date = TimestampParser.date(value) {
                    Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.caption).foregroundStyle(PhonePalette.muted)
                        .lineLimit(1).fixedSize()
                }
            }
            let layout = dynamicTypeSize.isAccessibilitySize ? AnyLayout(VStackLayout(alignment: .leading, spacing: 4)) : AnyLayout(HStackLayout(alignment: .firstTextBaseline, spacing: 8))
            if dynamicTypeSize.isAccessibilitySize {
                VStack(alignment: .leading, spacing: 4) { meta(spaced: false) }
            } else {
                ViewThatFits(in: .horizontal) {
                    HStack(alignment: .firstTextBaseline, spacing: 8) { meta(spaced: true) }
                    VStack(alignment: .leading, spacing: 4) { meta(spaced: false) }
                }
            }
            if kind == .unavailable, let reason = session.unavailableReason {
                Text(reason).font(.caption).foregroundStyle(PhonePalette.muted)
            } else if kind == .working, let activity = session.activity {
                PhoneWorkingNote(activity: activity)
            }
            if let held = HeldWorkLine.of(session) {
                PhoneHeldWorkLine(line: held).accessibilityIdentifier("session-held-work-" + session.id)
            }
            if session.queueCount > 0 || session.isReadOnly || (family?.workingSubagents ?? 0) > 0 || (family?.workers?.count ?? 0) > 0 || !session.sharedHolders.isEmpty {
                layout {
                    if let family, family.workingSubagents > 0 {
                        Label(SessionFamilies.subagentSummary(family), systemImage: "person.2")
                            .font(.caption)
                            .foregroundStyle(PhonePalette.working)
                    }
                    if let workers = family?.workers, workers.count > 0 {
                        if workers.needsYou > 0 {
                            Label(SessionFamilies.workerSummary(workers), systemImage: "person.3").font(.caption).foregroundStyle(PhonePalette.attention)
                        } else if workers.working > 0 {
                            Label(SessionFamilies.workerSummary(workers), systemImage: "person.3").font(.caption).foregroundStyle(PhonePalette.working)
                        } else {
                            Label(SessionFamilies.workerSummary(workers), systemImage: "person.3").font(.caption)
                        }
                    }
                    if session.queueCount > 0 { Label("\(session.queueCount) waiting", systemImage: "tray").font(.caption) }
                    if !session.sharedHolders.isEmpty { Label("\(session.sharedHolders.count) terminals", systemImage: "terminal").font(.caption) }
                    if session.isReadOnly { Label("Read only", systemImage: "lock").font(.caption) }
                }.foregroundStyle(PhonePalette.muted)
            }
        }
        .padding(.vertical, 8)
        .accessibilityElement(children: .combine)
    }
}

/// A one-line host label for remote sessions and projects. It truncates
/// rather than wrapping so it never squeezes the row's status.
struct IOSHostBadge: View {
    let name: String
    var unavailable = false

    var body: some View {
        HStack(spacing: 3) {
            Image(systemName: unavailable ? "wifi.slash" : "server.rack").imageScale(.small)
            Text(name).lineLimit(1).truncationMode(.tail)
        }
        .font(.caption)
        .foregroundStyle(unavailable ? AnyShapeStyle(PhonePalette.failed) : AnyShapeStyle(PhonePalette.muted))
        .frame(maxWidth: 110, alignment: .leading)
        .fixedSize(horizontal: false, vertical: true)
        .layoutPriority(-1)
    }
}
