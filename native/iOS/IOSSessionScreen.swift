import SwiftUI

struct SessionScreen: View {
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    private var compactHeight: Bool { verticalSizeClass == .compact && !typeSize.isAccessibilitySize }
    var model: AgenthailIOSModel
    @EnvironmentObject private var voiceCalls: VoiceCallCoordinator
    @Environment(\.horizontalSizeClass) private var sizeClass
    @Environment(\.dynamicTypeSize) private var typeSize
    let session: SessionState
    private var liveSession: SessionState { model.session(session.id) ?? session }
    @State private var showingInfo = false
    @State private var showingVoice = false
    @State private var showingNow = false
    @State private var shownRequest: String?
    @State private var activityOnly = false
    @AppStorage(AgentFold.defaultsKey) private var expandAgents = false
    @AppStorage(TranscriptFocus.defaultsKey) private var focus = false
    @State private var followingLatest = true
    @State private var edge = TranscriptEdge()
    @State private var queueTail = QueueTail()
    @State private var landed = false
    @State private var turnReveal: Task<Void, Never>?
    @State private var opening = OpeningRebuild()
    @State private var rowsGeneration = 0
    @State private var overflows = false
    @State private var jumps = 0
    @State private var userScrolling = false
    @State private var position = ScrollPosition()
    @State private var olderPlace = OlderPlaceHold()
    @State private var memo = TimelineMemo()
    private static let rowSpacing: CGFloat = 12

    private var displayTitle: String {
        session.subagent == nil ? SessionStyle.title(session) : model.cells.title(of: session)
    }

    private var detail: SessionDetail? {
        model.selectedDetail?.session.id == session.id ? model.selectedDetail : nil
    }
    private var items: [TimelineItem] {
        memo.items(latest: detail?.timeline?.items ?? [], older: model.olderActivity)
    }
    private var groups: [TimelineGroup] {
        memo.groups(latest: detail?.timeline?.items ?? [], older: model.olderActivity, foldAgents: !expandAgents, focus: focus)
    }

    static func showsActivity(timeline: SessionTimeline, itemCount: Int, activityCursor: Int64?, exchangeCount: Int) -> Bool {
        timeline.unavailableReason == nil && (itemCount > 0 || (activityCursor ?? 0) > 0 || exchangeCount == 0)
    }

    private var firstRowID: String? {
        activityOnly ? items.first.map { TranscriptRowID.item($0.id) } : groups.first?.id
    }

    private var latestRowID: String? {
        guard let detail, let timeline = detail.timeline else { return nil }
        let items = items
        guard Self.showsActivity(timeline: timeline, itemCount: items.count, activityCursor: model.activityCursor, exchangeCount: detail.exchangeCount ?? 0) else { return nil }
        return activityOnly ? items.last.map { TranscriptRowID.item($0.id) } : groups.last?.id
    }

    private func scrollToLatest() {
#if DEBUG
        ScrollCount.value += 1
        if PreviewFollowStuck.enabled, landed { return }
#endif
        switch LatestScroll.target(overflows: overflows, latest: queueTail.lastShownID ?? latestRowID, pinned: position.viewID(type: String.self)) {
        case .row(let id): position.scrollTo(id: id, anchor: .bottom)
        case .edge: position.scrollTo(edge: .bottom)
        }
    }

    // Older pages load until the turn is found, a few at most, so a tap on an old notification still lands somewhere sensible.
    private func revealOpenedTurn() async -> Bool? {
        let screen = session.id
        guard let turn = model.takeOpenedTurn(screen) else { return nil }
        var found = TurnReveal.find(turn, in: shownItems)
        var pages = 0
        while found.settled == nil, pages < TurnReveal.olderPages {
            guard !Task.isCancelled, model.selectedSessionID == screen else { return nil }
            if model.loadingOlderActivity {
                try? await Task.sleep(for: .milliseconds(100))
                found = TurnReveal.find(turn, in: shownItems)
                continue
            }
            guard let cursor = model.activityCursor, cursor > 0 else { break }
            await model.loadOlderActivity()
            found = TurnReveal.find(turn, in: shownItems)
            guard model.activityCursor != cursor else { break }
            pages += 1
        }
        guard !Task.isCancelled, model.selectedSessionID == screen else { return nil }
        guard let item = found.settled, let target = turnRow(item) else { return false }
        olderPlace.release()
        followingLatest = false
        position.scrollTo(id: target, anchor: .top)
        return true
    }

    private func startTurnReveal() {
        turnReveal?.cancel()
        turnReveal = Task {
            guard await revealOpenedTurn() == false else { return }
            followingLatest = true
            scrollToLatest()
        }
    }

    private var shownItems: [TimelineItem] {
        activityOnly ? items : groups.flatMap(\.items)
    }

    private func turnRow(_ item: String) -> String? {
        activityOnly ? TranscriptRowID.item(item) : TurnReveal.row(of: item, in: groups)
    }

    private func rebuildOpeningRows() {
        guard followingLatest, !userScrolling else { return }
#if DEBUG
        BodyRuns.hit("rowsRebuilt")
#endif
        rowsGeneration += 1
    }

    private func openingRowAppeared(_ id: String) {
        opening.rowAppeared(id)
        opening.fire(latest: latestRowID, rebuildOpeningRows)
    }

    private func settleAtLatest() async {
        for _ in 0..<5 {
            try? await Task.sleep(for: .milliseconds(50))
            guard followingLatest else { return }
            scrollToLatest()
        }
    }

    private func goToLatest() {
#if DEBUG
        BodyRuns.hit("jumpedToLatest")
        PreviewFollowStuck.enabled = false
#endif
        olderPlace.release()
        followingLatest = true
        jumps += 1
        position.scrollTo(edge: .bottom)
        Task { await settleAtLatest() }
    }

    private var showQueuedRow: RowAction<Void, Void>? {
        guard queueTail.lastShownID != nil else { return nil }
        return RowAction(owner: session.id) { goToLatest() }
    }

    @ViewBuilder private var transcriptRows: some View {
        let connection = model.connectionState(for: session.id)
        if let error = connection.error {
            Label(error, systemImage: "wifi.exclamationmark").font(.callout).foregroundStyle(PhonePalette.muted)
        } else if connection.reconnecting {
            Label("Reconnecting live updates", systemImage: "arrow.triangle.2.circlepath").font(.callout).foregroundStyle(PhonePalette.muted)
        }
        if model.loadingSession && detail == nil { ProgressView("Loading conversation") }
        if let error = model.sessionError {
            ContentUnavailableView {
                Label("Could not refresh conversation", systemImage: "exclamationmark.bubble")
            } description: { Text(error) } actions: {
                Button("Retry") { Task { await model.loadSession(session.id) } }
            }
        }
        if let detail {
            if let warning = detail.transcriptWarning {
                Label(warning, systemImage: "exclamationmark.bubble").font(.footnote).foregroundStyle(PhonePalette.muted)
            }
            if let timeline = detail.timeline,
               Self.showsActivity(timeline: timeline, itemCount: items.count, activityCursor: model.activityCursor, exchangeCount: detail.exchangeCount ?? 0) {
                if (model.activityCursor ?? 0) > 0 {
                    Button {
                        followingLatest = false
                        let hold = olderPlace.hold(firstRowID)
                        Task {
                            await model.loadOlderActivity()
                            olderPlace.pageApplied(hold)
                            if followingLatest { await settleAtLatest() }
                        }
                    } label: {
                        if model.loadingOlderActivity { ProgressView("Loading older activity") }
                        else { Label("Load older activity", systemImage: "arrow.up") }
                    }.disabled(model.loadingOlderActivity).frame(minHeight: 44)
                }
                if let error = model.olderActivityError { Text(error).font(.footnote).foregroundStyle(PhonePalette.muted) }
                if timeline.truncated {
                    Label("Showing recent activity. Older activity or long output has been shortened.", systemImage: "text.badge.ellipsis")
                        .font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                if activityOnly {
                    let leading = items.first?.id
                    ForEach(items) { item in
                        let row = TranscriptRowID.item(item.id)
                        LiveTimelineRow(item: item, compactContext: false, bodyLoader: { item in await model.retainedSessionBody(for: item) }, attachmentLoader: { item in await model.displayableAttachment(for: item, sessionID: session.id) }).rebuilt(row, generation: rowsGeneration).tracksOlderPlace(row, leading: item.id == leading, in: olderPlace).probesScrollJump(row).onAppear { if item.id == items.last?.id { openingRowAppeared(row) } }.id(row)
                    }
                } else {
                    let groups = self.groups
                    ForEach(groups) { group in
                        CompactActivityGroup(group: group, owner: session.id, attachmentLoader: { item in await model.displayableAttachment(for: item, sessionID: session.id) }) { followingLatest = false }
                            .equatable()
                            .padding(.top, group.tightAbove ? AgentFoldGroups.tightRunGap - Self.rowSpacing : 0)
                            .rebuilt(group.id, generation: rowsGeneration)
                            .tracksOlderPlace(group.id, leading: group.id == groups.first?.id, in: olderPlace).probesScrollJump(group.id)
                            .countsAppearances("groupAppeared")
                            .countsDisappearances("groupDisappeared")
                            .onAppear { if group.id == groups.last?.id { openingRowAppeared(group.id) } }.id(group.id)
                    }
                }
                if items.isEmpty { ContentUnavailableView("Ready for your instruction", systemImage: "bubble.left", description: Text("Messages and agent activity will appear here.")) }
                else if focus, !activityOnly, groups.isEmpty {
                    Text("No messages yet. Focus hides tool activity.").font(.footnote).foregroundStyle(PhonePalette.muted)
                        .accessibilityIdentifier("focus-empty")
                }
#if DEBUG
                if let space = PreviewStream.trailingSpace { Color.clear.frame(height: space) }
#endif
            } else {
                if let reason = detail.timeline?.unavailableReason {
                    Label(reason, systemImage: "info.circle").font(.footnote).foregroundStyle(PhonePalette.muted)
                } else {
                    Text("Message history. Detailed activity is not available for this session.").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                if detail.transcriptTruncated == true {
                    Text("Older or oversized messages were shortened.").font(.footnote).foregroundStyle(PhonePalette.muted)
                }
                let exchanges = detail.exchanges ?? []
                ForEach(Array(exchanges.enumerated()), id: \.offset) { _, exchange in
                    if !exchange.user.isEmpty { IOSMessage(label: exchange.promptLabel, text: exchange.user, timestamp: exchange.timestamp) }
                    if !exchange.assistant.isEmpty { IOSMessage(label: "Assistant", text: exchange.assistant, timestamp: exchange.timestamp) }
                }
                if exchanges.isEmpty {
                    ContentUnavailableView("No messages yet", systemImage: "bubble.left", description: Text("Messages will appear here as the agent works."))
                }
            }
        }
    }

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("SessionScreen")
        let _ = detail == nil ? BodyRuns.hit("SessionScreenNoDetail") : ()
#endif
        ScrollView {
            LazyVStack(alignment: .leading, spacing: Self.rowSpacing) {
                Group {
                    transcriptRows.readingTypeSize()
                    if let detail { QueueTailRows(model: model, tail: $queueTail, readOnly: detail.readOnly, generation: rowsGeneration) }
                }.transcriptScrimRow()
                .environment(\.sentFiles, SentFileContext(hostName: model.hostName(forSession: session.id)) { [model, id = session.id] item in try await model.sentFile(item, sessionID: id) })
            }
            .environment(\.editMessage, detail.map { $0.capabilities.send && !$0.readOnly } == true ? RowAction(owner: session.id) { model.editAndResend($0) } : nil)
            .environment(\.revealRow, RowAction(owner: session.id) {
                olderPlace.release()
                position.scrollTo(y: $0.wrappedValue)
            })
            .environment(\.transcriptRows, TranscriptRows(memory: model.rowMemory, session: session.id))
            .environment(\.timelineCells, detail == nil ? nil : model.timelineCells)
            .environment(\.toolCallStates, ToolCallStates(items: items, active: session.isWorking || session.isWaitingOnYou))
            .environment(\.expandAgentMessages, expandAgents)
            .environment(\.cachedAttachment, RowAction(owner: session.id) { [model, id = session.id] in model.cachedAttachment(for: $0, sessionID: id) })
            .frame(maxWidth: SessionStyle.readingWidth, alignment: .leading)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 20)
            .padding(.vertical, 16)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .transcriptScrimContent()
            .id(jumps)
        }
        .scrollPosition($position)
        .scrollJumpProbe()
        .accessibilityIdentifier("session-timeline")
        .phoneBackdrop(.transcript)
        .defaultScrollAnchor(.bottom, for: .initialOffset)
        .defaultScrollAnchor(followingLatest && !PreviewFollowStuck.enabled ? .bottom : nil, for: .sizeChanges)
        .defaultScrollAnchor(.top, for: .alignment)
        .scrollDismissesKeyboard(.interactively)
        .onScrollPhaseChange { _, phase in
#if DEBUG
            guard !ProcessInfo.processInfo.arguments.contains("--preview-session") || PreviewStream.follows else { return }
#endif
            if phase == .interacting {
                if !userScrolling { edge.beginDrag(following: followingLatest) }
                olderPlace.release()
                userScrolling = true
                followingLatest = false
            } else if phase == .idle && userScrolling {
                let after = edge.endDrag()
                followingLatest = after.follow
                userScrolling = false
                if after.catchUp { scrollToLatest() }
            }
        }
        // Raw edges: a derived value that repeats across a landing can skip its last action.
        .onScrollGeometryChange(for: TranscriptEnd.self) { TranscriptEnd($0) } action: { _, end in
#if DEBUG
            guard !ProcessInfo.processInfo.arguments.contains("--preview-session") || PreviewStream.follows else { return }
#endif
            edge.update(end)
        }
        .onScrollGeometryChange(for: CGFloat.self) { geometry in
            geometry.contentSize.height - geometry.visibleRect.height
        } action: { _, overflow in
            overflows = overflow > 0
        }
        .onScrollGeometryChange(for: TranscriptEnd.self) { geometry in
            olderPlace.read(geometry)
            return TranscriptEnd(geometry)
        } action: { old, new in
            guard new.contentHeight != old.contentHeight else { return }
            olderPlace.contentChanged()
            if landed, followingLatest, !userScrolling, new.contentHeight > old.contentHeight, new.distance >= TranscriptEdge.reach { scrollToLatest() }
        }
        .onAppear { olderPlace.scroll = { [position = $position] in position.wrappedValue = $0 } }
        .refreshable { await model.refreshSession(session.id) }
        .task(id: "\(RequestSync.viewKey(liveSession))|\(model.selectedSessionID == session.id)") { await model.syncRequests(for: liveSession) }
        .onChange(of: liveSession.ask) { model.settleRetiredAsks(liveSession) }
        // Not in the bottom bar: growing the bar would move the transcript and flip atLatest, which hides it again.
        .overlay(alignment: .bottom) {
            if detail != nil { JumpToLatestControl(edge: edge, following: followingLatest, action: goToLatest) }
        }
#if DEBUG
        .overlay(alignment: .topLeading) {
            ScrollCountLabel()
            if ScrollJumpProbe.enabled { ScrollJumpLabel() }
        }#endif
        .edgeBar(.top, spacing: 0) {
            if compactHeight { Color.clear.frame(height: 0) }
            else if let detail { SessionSummary(detail: detail, session: liveSession, openDetails: RowAction(owner: session.id) { showingInfo = true }).equatable() }
        }
        .edgeBar(.bottom) {
            VStack(spacing: 0) {
                if let detail, let waiting = detail.session.waitingOn, ElsewhereWaitCard.shows(waiting, readOnly: detail.readOnly, respond: detail.capabilities.respond) {
                    ElsewhereWaitCard(waiting: waiting, place: detail.waitingPlace ?? .elsewhere,
                                      surface: session.surface, hostName: session.runtime?.hostName, accent: AnyShapeStyle(PhonePalette.accentText),
                                      open: session.runtime?.focusable == true ? { model.focusSession(session) } : nil,
                                      restart: { model.stopAndResume(session.id) })
                        .id(waiting.id)
                        .padding(.horizontal)
                        .padding(.top, 6)
                } else if let detail, !detail.readOnly, let offered = model.sessionRequests?.offered(pendingAsk: liveSession.pendingAskID, open: liveSession.pendingAsks, retired: model.retiredAsks.retired(in: liveSession.id)), let request = offered.first(where: { $0.id == shownRequest }) ?? offered.first {
                    if offered.count > 1, let index = offered.firstIndex(of: request) {
                        RequestSwitcher(index: index, count: offered.count) { shownRequest = offered[$0].id }
                            .padding(.horizontal)
                            .padding(.top, 6)
                    }
                    RequestCard(request: request, answering: model.answeringRequest != nil, accent: AnyShapeStyle(PhonePalette.accentText),
                                respond: { decision in Task { await model.respond(to: request, sessionID: session.id, decision: decision) } },
                                leadNow: request.replacedBy.map { model.leadName($0, requestFrom: session.id) },
                                withdraw: request.replacedBy == nil ? nil : { Task { await model.withdraw(request, sessionID: session.id) } },
                                asker: liveSession.displayName, attention: AnyShapeStyle(PhonePalette.attention), actionTint: AnyShapeStyle(PhonePalette.accent),
                                agentSurface: liveSession.surface)
                    .id(request.id)
                    .padding(.horizontal)
                    .padding(.top, 6)
                } else if let receipt = model.retiredAsks.receipt(for: liveSession) {
                    AnswerReceiptRow(receipt: receipt)
                        .padding(.horizontal)
                        .padding(.top, 6)
                }
                if let detail {
                    if detail.readOnly || (!detail.capabilities.send && !detail.capabilities.steer) {
                        Label(detail.readOnlyReason.isEmpty ? "This session does not support messages." : detail.readOnlyReason, systemImage: "lock")
                            .font(.footnote).foregroundStyle(PhonePalette.muted)
                            .padding(.horizontal, 14).padding(.vertical, 10)
                            .floatingGlass(in: Capsule())
                            .padding()
                    } else {
                        TurnNoticeCard(sessionID: liveSession.id, notice: liveSession.composerTurnNotice)
                        if let move = liveSession.move {
                            Label("\(move.label). \(SessionMove.composerNote).", systemImage: "arrow.right.circle")
                                .font(.footnote).foregroundStyle(PhonePalette.muted)
                                .padding(.horizontal, 14)
                                .accessibilityIdentifier("session-move-note")
                        }
                        IOSComposer(model: model, draft: model.draft, store: model.store, session: session, context: ComposerContext(detail), showQueuedRow: showQueuedRow)
                            .equatable()
                            .countsAppearances("composerAppeared")
                    }
                }
            }
        }
        .countsScreenChurn()
        .toolbar(ConversationFlow.splits(sizeClass, typeSize) ? .automatic : .hidden, for: .tabBar)
        .navigationTitle(displayTitle)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                if compactHeight, let detail {
                    Button { showingInfo = true } label: {
                        HStack(spacing: 8) {
                            Text(displayTitle).font(.body.weight(.semibold)).lineLimit(1)
                            SessionStatusLabel(detail: detail, session: liveSession).equatable().labelStyle(.titleAndIcon).lineLimit(1)
                            Text(detail.model ?? SessionStyle.agentName(detail.session.surface)).font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityHint("Open session details, context, model and controls")
                    .accessibilityIdentifier("session-summary")
                } else {
                    VStack(spacing: 2) {
                        Text(displayTitle).font(.body.weight(.semibold)).lineLimit(1)
                        if let cwd = detail?.session.cwd, !cwd.isEmpty {
                            Text(PathNames.last(cwd) + " · " + (session.host?.name ?? "computer")).font(.caption2).foregroundStyle(PhonePalette.muted).lineLimit(1)
                        }
                    }
                }
            }
            if voiceCalls.model?.hasLocalCall == true {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        voiceCalls.open()
                        showingVoice = true
                    } label: { Image(systemName: "phone.fill").foregroundStyle(PhonePalette.working) }
                    .accessibilityLabel("Return to call")
                    .accessibilityIdentifier("return-to-call")
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                IOSNowButton(model: model, sessionID: session.id, showing: $showingNow)
            }
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button("Session details", systemImage: "info.circle") { showingInfo = true }
                    Picker("Transcript view", selection: $activityOnly) {
                        Text("Chat").tag(false)
                        Text("All events").tag(true)
                    }
                    Toggle("Focus", systemImage: "bubble.left.and.text.bubble.right", isOn: $focus)
                        .disabled(activityOnly)
                        .accessibilityIdentifier("session-focus")
                    Toggle("Expand agent messages", systemImage: "text.bubble", isOn: $expandAgents)
                    IOSPinButton(model: model, session: liveSession)
                    if model.canCallByVoice(session) {
                        Button("Call this session", systemImage: "phone.fill") {
                            voiceCalls.open(targetID: model.voiceTargetID(for: session))
                            showingVoice = true
                        }
                    }
                } label: { Image(systemName: "ellipsis") }
                .accessibilityLabel("Session menu")
                .disabled(detail == nil)
            }
        }
        .sheet(isPresented: $showingInfo) {
            if let liveDetail = model.selectedDetail, liveDetail.session.id == session.id {
                SessionInspector(model: model, session: session, initialDetail: liveDetail)
            }
        }
        .sheet(isPresented: $showingNow) {
            PhoneHappeningNow(model: model, session: liveSession, items: items) { model.openSession($0) }
        }
        .sheet(isPresented: $showingVoice) {
            if let call = voiceCalls.model {
                AgenthailVoiceOperatorSheet(model: call, rowMemory: model.rowMemory) { model.openSession(model.voiceSessionID($0)) }
            }
        }
        .task(id: session.id) {
            olderPlace.release()
            let startsAtOldest = ProcessInfo.processInfo.arguments.contains("--preview-reading-top")
            followingLatest = !startsAtOldest
            let open = opening.open(following: followingLatest)
            edge.atLatest = !startsAtOldest
            landed = false
            await model.loadSession(session.id)
            if startsAtOldest, let first = firstRowID {
                position.scrollTo(id: first, anchor: .top)
            } else if followingLatest, await revealOpenedTurn() != true {
                scrollToLatest()
            }
            landed = true
            opening.land(open)
            opening.fire(latest: latestRowID, rebuildOpeningRows)
            if model.openedTurns[session.id] != nil { startTurnReveal() }
        }
        .onChange(of: detail?.timeline?.items) { _, _ in
            if followingLatest { scrollToLatest() }
        }
        .onChange(of: model.openedTurns[session.id]) { _, turn in
            guard landed, turn != nil else { return }
            startTurnReveal()
        }
        .onAppear { model.sessionAppeared(session.id) }
        .onChange(of: session.id) { old, new in
            turnReveal?.cancel()
            model.sessionDisappeared(old)
            model.sessionAppeared(new)
        }
        .onDisappear {
            turnReveal?.cancel()
            olderPlace.release()
            model.sessionDisappeared(session.id)
        }
        .modifier(QueueTailTracking(model: model, tail: $queueTail, session: session, timeline: items))
        .onChange(of: queueTail.shownIDs) { _, _ in
            if landed, followingLatest { scrollToLatest() }
        }
        .onChange(of: detail?.exchanges?.last?.assistant) { _, _ in
            if followingLatest { scrollToLatest() }
        }
    }
}
