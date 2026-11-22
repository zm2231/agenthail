import SwiftUI

struct IOSComposer: View, @MainActor Equatable {
    var model: AgenthailIOSModel
    @ObservedObject var draft: ComposerDraft
    @ObservedObject var store: SessionStore
    @Environment(\.dynamicTypeSize) private var dynamicTypeSize
    @Environment(\.verticalSizeClass) private var verticalSizeClass
    let session: SessionState
    let context: ComposerContext
    let showQueuedRow: RowAction<Void, Void>?
    @State private var showingPrompts = false
    @State private var completionState = CompletionState(session: "", caret: 0)
    @State private var editor = IOSComposerEditor()

    static func == (lhs: Self, rhs: Self) -> Bool {
#if DEBUG
        BodyRuns.compared("composer", [
            "refs": lhs.model === rhs.model && lhs.draft === rhs.draft && lhs.store === rhs.store,
            "session": lhs.session == rhs.session, "context": lhs.context == rhs.context, "queuedRow": lhs.showQueuedRow == rhs.showQueuedRow,
        ])
#endif
        return lhs.model === rhs.model && lhs.draft === rhs.draft && lhs.store === rhs.store
            && lhs.session == rhs.session && lhs.context == rhs.context && lhs.showQueuedRow == rhs.showQueuedRow
    }

#if DEBUG
    private func probeComposerInputs() {
        let id = session.id
        BodyRuns.changed("in.composer.session", id, session)
        BodyRuns.changed("in.composer.context", id, context)
        BodyRuns.changed("in.composer.queuedRow", id, showQueuedRow)
        BodyRuns.observe("obs.composer.receipt", id) {
            _ = model.deliveryStatus[session.id] != nil && model.composerSession.failedSends[session.id] == nil && model.composerSession.pendingSends[session.id]?.contains(where: \.unconfirmed) != true
        }
        BodyRuns.observe("obs.composer.attachments", id) { _ = model.composerSession.composerAttachments.isEmpty }
        BodyRuns.observe("obs.composer.attachmentProblem", id) { _ = model.composerSession.attachmentProblem }
        BodyRuns.observe("obs.composer.stash", id) { _ = model.composerSession.stashes[session.id] }
        BodyRuns.observe("obs.composer.steering", id) { _ = steering }
        BodyRuns.observe("obs.composer.sending", id) { _ = sending }
        BodyRuns.observe("obs.composer.canSend", id) { _ = canSend }
        BodyRuns.watch("pub.composer.draft", draft)
        BodyRuns.watch("pub.composer.store", store)
    }
#endif

    private var steering: Bool { context.busy && !context.waiting && context.capabilities.steer && model.busyDelivery(for: context.sessionID) == "steer" }
    private var sending: Bool { model.composerSession.pendingSends[session.id]?.first.map { !$0.unconfirmed } == true && draft.text.isEmpty }
    private var stashActionTitle: String {
        let empty = ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerSession.composerAttachments)
        if !empty { return model.composerSession.stashes[session.id] == nil ? "Stash draft" : "Swap with stashed draft" }
        return "Bring back stashed draft"
    }

    @ViewBuilder private var composerMenu: some View {
        Button { showingPrompts = true } label: { Label("Recent Prompts", systemImage: "clock.arrow.circlepath") }
        let empty = ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerSession.composerAttachments)
        if !empty || model.composerSession.stashes[session.id] != nil {
            Button { model.composerSession.toggleStash() } label: { Label(stashActionTitle, systemImage: "tray.and.arrow.down") }
        }
    }

    private var canSend: Bool {
        !sending && !model.pendingControls.contains(session.id)
            && !ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerSession.composerAttachments)
            && context.capabilities.send
    }

    private var compactHeight: Bool { verticalSizeClass == .compact && !dynamicTypeSize.isAccessibilitySize }
    private var attachShown: Bool { context.capabilities.attach && context.capabilities.send }

    private func textView(lines: ClosedRange<Int>, windowShare: CGFloat? = nil) -> some View {
        IOSComposerTextView(
            text: $draft.text,
            placeholder: steering ? "Steer this turn…" : "Message this agent…",
            label: steering ? "Instruction for the current turn" : "Message to this agent",
            lines: lines,
            windowShare: windowShare,
            pasteAttachments: attachShown && !sending ? { model.composerSession.paste($0) } : nil,
            editor: editor,
            onCaret: { caret in updateCompletion { $0.caret = caret } },
            draftID: session.id
        )
    }

    @ViewBuilder private var attachControl: some View {
        if attachShown {
            ComposerAttachmentMenu(model: model)
                .disabled(sending)
        }
    }

    @ViewBuilder private var turnControls: some View {
        attachControl
        if !context.readOnly, let settings = context.turnSettings, settings.setting(TurnSetting.mode) != nil {
            // The chips fold into a menu to fit, so they claim no width of their own when the footer decides whether one row fits.
            IOSTurnSettingChips(model: model, sessionID: session.id, settings: settings)
                .frame(minWidth: 0, idealWidth: 0, maxWidth: .infinity, alignment: .leading)
                .layoutPriority(1)
        }
    }

    @ViewBuilder private var sendControls: some View {
        if context.busy && context.capabilities.interrupt {
            Button { model.action("interrupt", session: session) } label: {
                Image(systemName: "stop.fill").font(.system(size: 17, weight: .semibold)).frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .background(Color.primary.opacity(0.08), in: Circle())
            .accessibilityLabel("Stop current turn")
            .disabled(model.pendingControls.contains(session.id))
        }
        Button { model.composerSession.send(to: session) } label: {
            if sending { ProgressView().frame(width: 44, height: 44) }
            else { Image(systemName: "arrow.up").font(.system(size: 18, weight: .semibold)).frame(width: 44, height: 44) }
        }
        .buttonStyle(.plain)
        .foregroundStyle(canSend ? AnyShapeStyle(.white) : AnyShapeStyle(PhonePalette.muted))
        .background(canSend ? AnyShapeStyle(PhonePalette.accent) : AnyShapeStyle(Color.clear), in: Circle())
        .overlay(Circle().strokeBorder(canSend ? AnyShapeStyle(Color.clear) : AnyShapeStyle(PhonePalette.muted), lineWidth: 1))
        .accessibilityLabel(steering ? "Send steering instruction" : "Send message")
        .disabled(!canSend)
        .contextMenu { composerMenu }
        .accessibilityAction(named: "Recent prompts") { showingPrompts = true }
        .accessibilityAction(named: stashActionTitle) { model.composerSession.toggleStash() }
    }

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("IOSComposer")
        let _ = probeComposerInputs()
#endif
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 6) {
                if let status = model.deliveryStatus[session.id], model.composerSession.failedSends[session.id] == nil, model.composerSession.pendingSends[session.id]?.contains(where: \.unconfirmed) != true {
                    ComposerReceipt(status: status, showQueuedRow: showQueuedRow.map { action in { action(()) } })
                }
                ComposerSendStatus(model: model, session: session)
                if let host = session.unreachableHostName {
                    Label("Will send when \(host) is back.", systemImage: "wifi.slash")
                        .font(.footnote).foregroundStyle(PhonePalette.muted).padding(.horizontal, 8)
                        .accessibilityIdentifier("composer-host-down")
                }
                if !model.composerSession.composerAttachments.isEmpty {
                    ComposerAttachmentStrip(model: model)
                }
                if let problem = model.composerSession.attachmentProblem {
                    Text(problem).font(.footnote).foregroundStyle(PhonePalette.failed).padding(.horizontal, 8)
                        .accessibilityIdentifier("attachment-problem")
                }
                if !session.sharedHolders.isEmpty {
                    IOSHolderPicker(model: model, session: session)
                }
                if model.composerSession.stashes[session.id] != nil {
                    let empty = ComposerAttachments.isEmpty(text: draft.text, attachments: model.composerSession.composerAttachments)
                    Button { model.composerSession.toggleStash() } label: {
                        Label(empty ? "Bring back stashed draft" : "Draft stashed · returns after you send", systemImage: "tray.and.arrow.down")
                            .font(.footnote).foregroundStyle(PhonePalette.muted)
                            .padding(.horizontal, 8).frame(minHeight: 44, alignment: .leading)
                    }
                    .buttonStyle(.plain)
                    .disabled(!empty)
                    .accessibilityIdentifier("composer-stash")
                }
                if !completion.items.isEmpty || completion.notice != nil {
                    CompletionRow(suggestions: completion.items, notice: completion.notice, choose: accept) {
                        updateCompletion { $0.dismissed = draft.text }
                    }
                }
                if compactHeight {
                    HStack(spacing: 8) {
                        attachControl
                        textView(lines: 1 ... 2).padding(.leading, attachShown ? 0 : 8)
                        if !context.readOnly, let settings = context.turnSettings, settings.setting(TurnSetting.mode) != nil {
                            IOSTurnSettingsMenu(model: model, sessionID: session.id, settings: settings)
                        }
                        sendControls
                    }
                } else {
                    textView(lines: dynamicTypeSize.isAccessibilitySize ? 1 ... 2 : 1 ... 6, windowShare: dynamicTypeSize.isAccessibilitySize ? nil : 0.4)
                        .padding(.horizontal, 8).padding(.top, 8)
                    let row = HStack(spacing: 8) { turnControls; Spacer(minLength: 0); sendControls }
                    if dynamicTypeSize.isAccessibilitySize {
                        ViewThatFits(in: .horizontal) {
                            row
                            VStack(alignment: .leading, spacing: 8) {
                                turnControls
                                HStack(spacing: 8) { Spacer(minLength: 0); sendControls }
                            }
                        }
                    } else {
                        row
                    }
                }
            }
            .padding(10)
            .floatingGlass(in: RoundedRectangle(cornerRadius: 26, style: .continuous))
            .overlay { RoundedRectangle(cornerRadius: 26).strokeBorder(Color.primary.opacity(0.06)) }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("composer")
        }
        .frame(maxWidth: SessionStyle.readingWidth)
        .frame(maxWidth: .infinity)
        .padding(.horizontal).padding(.vertical, 10)
        .sheet(isPresented: $showingPrompts) {
            RecentPromptsSheet(model: model, session: session) { text in
                draft.text = draft.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? text : draft.text + "\n\n" + text
            }
        }
        .task(id: mentionRequest) { await loadMentionFiles(mentionRequest) }
    }

    private var scopedCompletion: CompletionState { completionState.scoped(to: session.id, draft: draft.text) }

    private func updateCompletion(_ change: (inout CompletionState) -> Void) {
        var state = scopedCompletion
        change(&state)
        completionState = state
    }

    private var completionToken: CompletionToken? {
        guard scopedCompletion.dismissed != draft.text else { return nil }
        return CompletionToken.at(draft.text, caret: min(scopedCompletion.caret, (draft.text as NSString).length))
    }

    private var mentionRequest: MentionRequest? {
        completionToken.flatMap { $0.trigger == .mention ? MentionRequest(session: session.id, query: $0.query) : nil }
    }

    private var completion: (items: [CompletionItem], notice: String?) {
        guard let token = completionToken else { return ([], nil) }
        let runtime = store.commands[session.id]
        let mentioning = token.trigger == .mention
        let files = scopedCompletion.files
        let items = ComposerCompletion.items(for: token, appCommands: [], runtime: runtime, sessions: mentioning ? ComposerCompletion.sessions(mentionable(token.query), from: session) : [], files: mentioning && files?.query == token.query ? files?.files ?? [] : [])
        return (items, ComposerCompletion.notice(for: token, runtime: runtime))
    }

    private func loadMentionFiles(_ request: MentionRequest?) async {
        guard let request else { return }
        try? await Task.sleep(for: .milliseconds(120))
        guard !Task.isCancelled else { return }
        async let sessions = model.mentionSessions(for: request.session, query: request.query)
        let found = await model.sessionFiles(request.session, query: request.query)
        let listed = await sessions
        guard !Task.isCancelled else { return }
        updateCompletion {
            $0.files = (request.query, found ?? [])
            $0.sessions = listed.map { (request.query, $0) }
        }
    }

    private func mentionable(_ query: String) -> [SessionState] {
        let listed = model.snapshot?.sessions ?? []
        guard let found = scopedCompletion.sessions, found.query == query else { return listed }
        let held = Set(listed.map(\.id))
        return listed + found.sessions.filter { !held.contains($0.id) }
    }

    private func accept(_ item: CompletionItem) {
        guard let token = completionToken else { return }
        let change = ComposerCompletion.edit(item, in: draft.text, at: token)
        editor.replace(change.range, with: change.replacement, caret: change.caret)
    }
}

extension EnvironmentValues {
    @Entry var editMessage: RowAction<String, Void>? = nil
}
