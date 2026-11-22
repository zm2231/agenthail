import SwiftUI

struct SessionSummary: View, @MainActor Equatable {
    let status: SessionStatusLabel
    let modelName: String
    let usage: ContextState?
    let openDetails: RowAction<Void, Void>

    init(detail: SessionDetail, session: SessionState, openDetails: RowAction<Void, Void>) {
        status = SessionStatusLabel(detail: detail, session: session)
        modelName = detail.model ?? SessionStyle.agentName(detail.session.surface)
        usage = detail.context
        self.openDetails = openDetails
    }

    var body: some View {
        Button { openDetails(()) } label: {
            HStack(spacing: 8) {
                status.lineLimit(1).truncationMode(.tail).layoutPriority(2)
                ViewThatFits(in: .horizontal) {
                    Text(modelName).lineLimit(1).fixedSize()
                    Color.clear.frame(width: 0, height: 0)
                        .accessibilityElement()
                        .accessibilityLabel(modelName)
                }
                Spacer(minLength: 4)
                context.lineLimit(1).fixedSize().layoutPriority(1)
                Image(systemName: "chevron.down").font(.caption2)
            }
            .dynamicTypeSize(...DynamicTypeSize.xxxLarge)
            .font(.footnote)
            .foregroundStyle(PhonePalette.muted)
            .frame(maxWidth: SessionStyle.readingWidth)
            .frame(maxWidth: .infinity)
            .padding(.horizontal, 16)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityHint("Open session details, context, model and controls")
        .accessibilityIdentifier("session-summary")
    }
	@ViewBuilder private var context: some View {
		if let context = usage {
			Group {
				if let fraction = context.fraction {
					Text("\(context.windowEstimated == true ? "~" : "")\(Int(fraction * 100))% context").monospacedDigit()
				} else {
					Text("\(context.usedTokens.formatted()) tokens").monospacedDigit()
				}
			}
			.foregroundStyle(context.level == .full ? PhonePalette.failed : context.level == .high ? RoleStyle(.body) : PhonePalette.muted)
			.accessibilityLabel(context.spokenLabel)
        }
    }
}

struct SessionInspector: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let initialDetail: SessionDetail
    @Environment(\.dismiss) private var dismiss
    @State private var showingModels = false
    @State private var renamingClaude = false
    @State private var movingTo: LimitResumeState.MoveOffer?
    @State private var movingHost = false
    @State private var claudeName = ""
    @State private var adopting = false
    private var detail: SessionDetail {
        guard let liveDetail = model.selectedDetail, liveDetail.session.id == session.id else { return initialDetail }
        return liveDetail
    }
    private var liveSession: SessionState { model.session(session.id) ?? session }
    var body: some View {
        NavigationStack {
            List {
                Section {
                    LabeledContent("Agent", value: detail.session.surface.capitalized)
                    LabeledContent("Status", value: SessionDisplayKind.label(liveSession, waitingPlace: detail.waitingPlace, limit: detail.limitLabel))
                    if let value = detail.model { LabeledContent("Model", value: value) }
                    if let role = AgentRoleFacts.sessionRoleRow(detail.role) { LabeledContent("Role", value: role.value).accessibilityIdentifier("session-role") }
                    if let configRoot = liveSession.claudeConfigRoot { LabeledContent("Config folder", value: configRoot.summary) }
                    if liveSession.canAdopt { Button("Open in Agenthail", systemImage: "arrow.down.app") { adopting = true } }
                    if let value = detail.session.cwd, !value.isEmpty { LabeledContent("Workspace", value: value).textSelection(.enabled) }
                    if let value = detail.session.source { LabeledContent("Source", value: value) }
                    if let value = detail.session.transport { LabeledContent("Connection", value: value) }
                    if let value = detail.readSource, !value.isEmpty { LabeledContent("Activity source", value: value) }
                    if let value = detail.readError, !value.isEmpty { LabeledContent("Activity warning", value: value).foregroundStyle(PhonePalette.muted) }
                    LabeledContent("Session ID", value: detail.session.id).textSelection(.enabled)
                } header: {
                    Text("Session").accessibilityIdentifier("session-inspector-anchor")
                }
                if let background = liveSession.backgroundTasks {
                    Section("Background work") {
                        Text(background.summary)
                        ForEach(background.tasks ?? []) { task in
                            Text(task.line).foregroundStyle(task.ambient == true ? AnyShapeStyle(PhonePalette.muted) : AnyShapeStyle(.primary))
                        }
                        if let note = background.sourceNote { Text(note).font(.footnote).foregroundStyle(PhonePalette.muted) }
                    }
                }
                IOSSessionOperations(model: model, session: liveSession, readOnlyReason: detail.readOnlyReason)
                Section("Context") {
                    if let context = detail.context {
                        LabeledContent("Used tokens", value: context.usedTokens.formatted())
						LabeledContent(context.windowEstimated == true ? "Estimated window" : (context.contextWindowSource == "configured" ? "Configured window" : "Context window"), value: context.contextWindow > 0 ? context.contextWindow.formatted() : "Unavailable")
                        if let value = context.inputTokens { LabeledContent("Input", value: value.formatted()) }
                        if let value = context.cachedInputTokens { LabeledContent("Cached input", value: value.formatted()) }
                        if let value = context.outputTokens { LabeledContent("Output", value: value.formatted()) }
                        if let value = context.reasoningOutputTokens { LabeledContent("Reasoning output", value: value.formatted()) }
                        if let value = context.cumulativeTokens { LabeledContent("Cumulative tokens", value: value.formatted()) }
                        LabeledContent("Compactions", value: context.compactionCount.formatted())
                        if let value = context.reclaimedTokens { LabeledContent("Tokens reclaimed", value: value.formatted()) }
                        if context.compacting { Label("Compacting context", systemImage: "arrow.down.right.and.arrow.up.left") }
                    } else { Text("This agent has not reported context usage.").foregroundStyle(PhonePalette.muted) }
                    if !detail.readOnly, let compact = detail.compactSettings, !compact.isEmpty {
                        IOSCompactSettingRows(model: model, sessionID: session.id, settings: compact)
                    }
                }
                SessionEditingControls(model: model, detail: detail)
                if let goal = detail.goal.map(GoalFacts.init) {
                    Section("Goal") {
                        if let objective = goal.objective { Text(objective).textSelection(.enabled) }
                        LabeledContent("Status", value: goal.status)
                            .foregroundStyle(goal.needsAttention ? AnyShapeStyle(PhonePalette.attention) : AnyShapeStyle(.primary))
                        ForEach(goal.rows, id: \.label) { LabeledContent($0.label, value: $0.value) }
                        if let reason = goal.reason { LabeledContent("Reason") { Text(reason).textSelection(.enabled) } }
                    }
                }
                if let runs = detail.claudeRuns, !runs.isEmpty {
                    Section("Claude runs") {
                        ForEach(runs) { run in
                            VStack(alignment: .leading, spacing: 4) {
                                Text(run.jobId).font(.body.weight(.medium)).textSelection(.enabled)
                                if let state = run.providerState, !state.isEmpty { LabeledContent("Provider state", value: state) }
                                if let type = run.runType, !type.isEmpty { LabeledContent("Run type", value: type) }
                                if let updated = run.updatedAt, !updated.isEmpty { LabeledContent("Updated", value: updated) }
                                if !run.recordPath.isEmpty { Text(run.recordPath).font(.footnote.monospaced()).foregroundStyle(PhonePalette.muted).textSelection(.enabled) }
                            }
                        }
                    }
                }
                let children = model.subagents(of: session.id)
                let subagentsLoading = model.fetchedSubagents.loading(liveSession)
                let subagentsError = model.fetchedSubagents.errors[session.id]
                if !children.isEmpty || subagentsLoading || subagentsError != nil {
                    Section("Subagents") {
                        if subagentsLoading || subagentsError != nil {
                            Text(subagentsError ?? "Loading subagents").foregroundStyle(PhonePalette.muted)
                        }
                        ForEach(children) { child in
                            Button {
                                dismiss()
                                model.openSession(child.id)
                            } label: {
                                HStack {
                                    Text(SessionFamilies.label(child)).lineLimit(1)
                                    Spacer(minLength: 0)
                                    if SessionDisplayKind.of(child) == .working { Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(PhonePalette.working) }
                                }
                            }
                            .accessibilityIdentifier("subagent-" + child.id)
                        }
                    }
                }
                if let links = detail.claudeSubagents, !links.isEmpty {
                    let list = ClaudeSubagentList(links, total: detail.claudeSubagentsTotal, expanded: model.showsAllClaudeSubagents)
                    Section {
                        ForEach(list.shown) { link in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack(alignment: .firstTextBaseline) {
                                    Text(link.title).font(.body.weight(.medium)).lineLimit(2)
                                    Spacer(minLength: 0)
                                    if link.working == true {
                                        Label("Working", systemImage: "waveform").font(.caption).foregroundStyle(PhonePalette.working)
                                    } else if let value = link.lastActive, let date = TimestampParser.date(value) {
                                        Text(date, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.caption).foregroundStyle(PhonePalette.muted)
                                    }
                                }
                                if let kind = link.kindLabel { Text(kind).font(.caption).foregroundStyle(PhonePalette.muted) }
                            }
                            .accessibilityElement(children: .combine)
                        }
                        if list.earlier > 0 {
                            Button(list.earlierLabel) { model.showAllClaudeSubagents() }
                        }
                    } header: {
                        Text("Claude subagents")
                    } footer: {
                        Text(list.summary)
                    }
                }
                if let errors = detail.metadataErrors, !errors.isEmpty {
                    Section("Metadata warnings") {
                        ForEach(MetadataWarnings.lines(errors), id: \.self) { line in
                            Text(line).textSelection(.enabled)
                        }
                    }
                }
                Section("Controls") {
                    IOSSessionProjectControls(model: model, session: session)
                    if session.runtime?.focusable == true {
                        Button("Open in host", systemImage: "arrow.up.forward.app") { model.focusSession(session); dismiss() }
                    }
                    if detail.readOnly { Label(detail.readOnlyReason, systemImage: "lock").font(.footnote) }
                    else {
                        if detail.capabilities.model {
                            let shown = detail.turnSettings?.setting(TurnSetting.model)
                            Button { showingModels = true } label: {
                                LabeledContent("Model", value: shown?.valueLabel ?? detail.model ?? "Default")
                            }
                            .accessibilityLabel(shown?.chipAccessibilityLabel ?? "Model, \(detail.model ?? "Default")")
                            .accessibilityIdentifier("session-model")
                            .disabled(model.pendingControls.contains(session.id))
                        }
                        if let effort = detail.turnSettings?.setting(TurnSetting.effort) {
                            Menu {
                                TurnSettingPicker(model: model, sessionID: session.id, setting: effort, options: effort.options)
                            } label: {
                                LabeledContent("Effort", value: effort.valueLabel)
                            }
                            .accessibilityLabel(effort.chipAccessibilityLabel)
                            .accessibilityIdentifier("session-effort")
                            .disabled(model.pendingControls.contains(session.id))
                        }
                        if detail.capabilities.permissionMode, let mode = detail.turnSettings?.setting(TurnSetting.mode) {
                            Menu {
                                Picker(mode.chipName, selection: Binding(get: { mode.value }) { id in
                                    guard let option = mode.quietOptions.first(where: { $0.id == id }), mode.pick(option) == .apply else { return }
                                    Task { await model.setTurnSetting(mode.key, to: option.id, sessionID: session.id) }
                                }) {
                                    ForEach(mode.quietOptions, id: \.id) { option in
                                        Text(option.label).tag(option.id)
                                    }
                                }
                                .pickerStyle(.inline)
                                .labelsHidden()
                            } label: {
                                LabeledContent("Permission mode", value: mode.valueLabel)
                            }
                            .accessibilityLabel(mode.chipAccessibilityLabel)
                            .accessibilityIdentifier("session-permission-mode")
                            .disabled(model.pendingControls.contains(session.id))
                        }
                        if detail.capabilities.commands {
                            Group {
                                Button("Check runtime status") { model.action("status", session: session) }
                                Button("Inspect context") { model.action("context", session: session) }
                                Button("Check usage") { model.action("usage", session: session) }
                                if detail.turnSettings?.setting(TurnSetting.effort) == nil {
                                    Menu("Change effort") {
                                        ForEach(["low", "medium", "high", "max", "auto"], id: \.self) { effort in
                                            Button(effort.capitalized) { model.action("effort", session: session, message: effort) }
                                        }
                                    }
                                }
                                Button("Rename Claude session") { claudeName = session.name; renamingClaude = true }
                            }.disabled(model.pendingControls.contains(session.id))
                        }
                        if let stop = detail.limitResume, !detail.readOnly {
                            Button("Resume now", systemImage: "play.fill") { model.action("limit-resume", session: session); dismiss() }
                            if stop.isWaiting { Button("Don't resume", systemImage: "pause.circle") { model.action("limit-resume-cancel", session: session) } }
                            if let offer = stop.moveTo { Button("Move to \(offer.name)", systemImage: "person.crop.circle.badge.checkmark") { movingTo = offer } }
                        }
                        IOSForkSessionButton(model: model, session: liveSession, dismiss: dismiss)
                        if model.canMove(liveSession) {
                            Button("Move to another computer", systemImage: "arrow.right.circle") { movingHost = true }
                                .accessibilityIdentifier("session-move")
                        }
                        if detail.capabilities.compact { Button("Compact context", systemImage: "arrow.down.right.and.arrow.up.left") { model.action("compact", session: session); dismiss() } }
                        if detail.capabilities.interrupt && detail.session.status == "busy" { Button("Stop current turn", systemImage: "stop.fill", role: .destructive) { model.action("interrupt", session: session); dismiss() } }
                        LabeledContent("Send messages", value: detail.capabilities.send ? "Available" : "Unavailable")
                        LabeledContent("Steer active turns", value: detail.capabilities.steer ? "Available" : "Unavailable")
                    }
                }
                if let output = model.controlOutput[session.id], !output.isEmpty {
                    Section("Control result") { Text(output).font(.footnote).textSelection(.enabled) }
                }
            }
            .navigationTitle("Session details").navigationBarTitleDisplayMode(.inline)
            .alert("Rename Claude session", isPresented: $renamingClaude) {
                TextField("Session name", text: $claudeName)
                Button("Cancel", role: .cancel) {}
                Button("Rename") { model.action("rename", session: session, message: claudeName) }
                    .disabled(claudeName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            } message: { Text("Changes the Claude session name, not its Agenthail alias.") }
            .confirmationDialog(movingTo.map { "Move this session to \($0.name)?" } ?? "", isPresented: Binding(get: { movingTo != nil }, set: { if !$0 { movingTo = nil } }), titleVisibility: .visible, presenting: movingTo) { offer in
                Button("Move to \(offer.name)") { model.moveToAccount(session, root: offer.root); dismiss() }
                Button("Cancel", role: .cancel) {}
            } message: { offer in
                Text(["It stops here and continues under that account.", offer.reason.map { "Chosen for \($0)." }].compactMap { $0 }.joined(separator: " "))
            }
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
            .sheet(isPresented: $movingHost) { IOSMoveSessionSheet(model: model, session: liveSession) { dismiss() } }
            .sheet(isPresented: $adopting) { IOSAdoptSessionSheet(model: model, session: model.routedSession(liveSession) ?? liveSession, sessionID: liveSession.id) }
            .task(id: SubagentFetchKey(liveSession, revision: model.fetchedSubagents.revision(session.id))) { await model.loadSubagents(of: liveSession) }
            .sheet(isPresented: $showingModels) {
                SearchableModelSelectionSheet(initialOptions: detail.modelOptions(), currentSelectedID: detail.selectedModelID, allowsDefault: false, onSelect: { value in
                    if let value {
                        Task { await model.setTurnSetting(TurnSetting.model, to: value, sessionID: session.id) }
                        dismiss()
                    }
                }, reload: { detail.modelOptions(catalog: try await model.creationModels(surface: session.surface, host: session.remoteHostID)) })
            }
        }
    }
}

func formatGoalDuration(_ seconds: Int) -> String {
    if seconds < 60 { return "\(seconds)s" }
    let minutes = seconds / 60
    let remainingSeconds = seconds % 60
    if minutes < 60 { return "\(minutes)m \(remainingSeconds)s" }
    return "\(minutes / 60)h \(minutes % 60)m"
}
