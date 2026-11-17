import SwiftUI

struct DecisionSections: View {
    var model: AgenthailIOSModel
    let inbox: DecisionInbox
    let open: (String) -> Void
    let answer: (DecisionItem) -> Void

    var body: some View {
        Group {
            ForEach(inbox.unreachable, id: \.self) { name in
                Label("\(name) isn't answering. Its decisions aren't shown.", systemImage: "wifi.exclamationmark")
                    .foregroundStyle(PhonePalette.muted).font(.footnote)
                    .accessibilityIdentifier("decisions-unreachable-\(name)")
            }
            ForEach(inbox.outdated, id: \.self) { name in
                Label("Update Agenthail on \(name) to see its decisions here.", systemImage: "arrow.up.circle")
                    .foregroundStyle(PhonePalette.muted).font(.footnote)
                    .accessibilityIdentifier("decisions-outdated-\(name)")
            }
            ForEach(DecisionGroup.allCases, id: \.self) { group in
                let items = inbox.items(in: group)
                if !items.isEmpty {
                    Section("\(group.title) · \(items.count)") {
                        ForEach(items) { row($0) }
                    }
                }
            }
            if !inbox.leadHandling.isEmpty {
                Section("Handled by a lead") {
                    ForEach(inbox.leadHandling) { item in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(name(of: item.sessionId)).font(.subheadline)
                            Text("\(name(of: item.heldBy)) is answering its prompt").font(.footnote).foregroundStyle(PhonePalette.muted)
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityIdentifier("decision-lead-\(item.sessionId)")
                    }
                }
            }
        }
    }

    private func row(_ item: DecisionItem) -> some View {
        let headline = headline(of: item)
        return VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 4) {
                    if let project = project(of: item) {
                        Text(project).font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1)
                    }
                    Spacer(minLength: 8)
                    if let since = item.sinceDate {
                        Text(since, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.footnote).foregroundStyle(PhonePalette.muted).fixedSize()
                    }
                }
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Button { open(item.sessionId) } label: {
                        Text(name(of: item.sessionId)).lineLimit(1).truncationMode(.middle)
                    }
                        .buttonStyle(.borderless)
                        .font(.footnote.weight(.medium))
                        .layoutPriority(1)
                        .accessibilityHint("Opens the session")
                        .accessibilityIdentifier("decision-session-link")
                    if let title = model.sessionTitle(item.sessionId) {
                        Text(title).font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1)
                    }
                }
                Text(headline).font(.headline).lineLimit(2)
                HStack(spacing: 8) {
                    if let holds = item.holdsUp, !holds.isEmpty {
                        Text("Holds up \(holds.count)").font(.caption.weight(.semibold)).foregroundStyle(PhonePalette.attention)
                    }
                    if item.openRequests.count > 1 {
                        Text("\(item.openRequests.count) requests").font(.caption).foregroundStyle(PhonePalette.muted)
                    }
                    if item.isParked {
                        if let tag = DecisionInbox.tag(item, sentTo: model.decisionFeed.replies[item.id]) {
                            Text(tag).font(.caption.weight(.semibold)).foregroundStyle(PhonePalette.muted).lineLimit(1)
                        }
                    } else if item.reason != headline {
                        Text(item.reason)
                            .font(.caption).foregroundStyle(PhonePalette.muted).lineLimit(2)
                    }
                }
        }
        .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
        .contentShape(Rectangle())
        .onTapGesture { answer(item) }
        .swipeActions(edge: .trailing) {
            Button(DecisionInbox.notNowTitle(item)) {
                Task { if let failure = await model.dismissDecision(item) { model.operationError = failure } }
            }
                .tint(PhonePalette.muted)
                .accessibilityIdentifier("decision-not-now-\(item.id)")
        }
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction { answer(item) }
        .accessibilityIdentifier("decision-\(item.id)")
    }

    private func headline(of item: DecisionItem) -> String {
        if let request = item.openRequests.first {
            return request.isAsk ? (request.title ?? request.summary) : request.summary
        }
        return item.isParked ? item.reason : name(of: item.sessionId)
    }

    private func project(of item: DecisionItem) -> String? {
        item.projectId.flatMap { model.projects[$0]?.state.displayName }
    }

    private func name(of sessionID: String) -> String {
        model.sessionName(sessionID)
    }
}

struct DecisionSheet: View {
    var model: AgenthailIOSModel
    let item: DecisionItem
    let title: String
    let open: () -> Void
    let done: () -> Void
    @State private var remaining: [SessionRequest] = []
    @State private var failure: String?
    @State private var reply = ""
    @State private var sending = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        Button(action: open) { Label(title, systemImage: "arrow.up.forward.square").lineLimit(1).truncationMode(.middle) }
                            .buttonStyle(.borderless)
                            .font(.subheadline.weight(.semibold))
                            .accessibilityHint("Opens the session")
                            .accessibilityIdentifier("decision-header-session-link")
                        if let subtitle = model.sessionTitle(item.sessionId) {
                            Text(subtitle).font(.footnote).foregroundStyle(PhonePalette.muted)
                        }
                    }
                    if !item.isParked {
                        Text(item.reason).font(.footnote).foregroundStyle(PhonePalette.muted)
                    }
                    if let holds = item.holdsUp, !holds.isEmpty {
                        Text("Holds up \(holds.map(name).formatted(.list(type: .and)))").font(.footnote).foregroundStyle(PhonePalette.attention)
                            .accessibilityIdentifier("decision-holds-up")
                    }
                    if let failure {
                        Label(failure, systemImage: "exclamationmark.triangle").font(.footnote).foregroundStyle(PhonePalette.failed)
                            .accessibilityIdentifier("decision-failure")
                    }
                    if let request = remaining.first {
                        if remaining.count > 1 {
                            Text("Request 1 of \(remaining.count)").font(.caption).foregroundStyle(PhonePalette.muted)
                        }
                        RequestCard(request: request, answering: model.answeringRequest != nil, accent: AnyShapeStyle(PhonePalette.accentText),
                                    respond: { decision in Task { await answer(request, decision) } },
                                    asker: title, attention: AnyShapeStyle(PhonePalette.attention), actionTint: AnyShapeStyle(PhonePalette.accent),
                                    agentSurface: model.session(item.sessionId)?.surface ?? "")
                            .id(request.id)
                        if let recommended = DecisionInbox.recommended(request) {
                            Button("Accept recommended") { Task { await answer(request, recommended) } }
                                .buttonStyle(.bordered)
                                .disabled(model.answeringRequest != nil)
                                .accessibilityIdentifier("decision-accept-recommended")
                        }
                    }
                    if item.isParked { parked }
                    Button(DecisionInbox.notNowTitle(current)) { Task { await dismiss() } }
                        .buttonStyle(.bordered)
                        .disabled(sending || model.answeringRequest != nil || model.decisionFeed.dismissing.contains(item.id))
                        .accessibilityHint("Takes this off Needs you without answering")
                        .accessibilityIdentifier("decision-not-now")
                }
                .padding()
            }
            .phoneBackdrop(.pane)
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Close", action: done) }
                ToolbarItem(placement: .primaryAction) { Button("Open session", action: open).accessibilityIdentifier("decision-open-session") }
            }
        }
        .onAppear { remaining = item.openRequests }
    }

    private func answer(_ request: SessionRequest, _ decision: RequestDecision) async {
        switch await model.answerDecision(request, sessionID: item.sessionId, decision: decision) {
        case .answered:
            failure = nil
            model.decisionFeed.answered(request, in: item.sessionId)
            next(after: request)
        case .failed(let result):
            failure = result.message
            if result.dropsCard {
                model.decisionFeed.answered(request, in: item.sessionId)
                next(after: request)
            }
        case .busy:
            break
        }
    }

    private func next(after request: SessionRequest) {
        remaining.removeAll { $0.id == request.id }
        if remaining.isEmpty { done() }
    }

    private func name(_ sessionID: String) -> String {
        model.sessionName(sessionID)
    }

    private var parked: some View {
        let work = item.coveredWork
        return VStack(alignment: .leading, spacing: 12) {
            Text(item.reason).font(.body.weight(.medium))
            VStack(alignment: .leading, spacing: 6) {
                Text(work.count > 1 ? "Covers \(work.count) items" : "Work item").font(.caption.weight(.semibold)).foregroundStyle(PhonePalette.muted)
                ForEach(work, id: \.key) { entry in
                    VStack(alignment: .leading, spacing: 2) {
                        Text(entry.workTitle ?? entry.workItem ?? "").font(.subheadline)
                        if entry.workTitle != nil, let id = entry.workItem {
                            Text(id).font(.caption2).foregroundStyle(.tertiary)
                        }
                    }
                    .accessibilityElement(children: .combine)
                }
            }
            .accessibilityIdentifier("decision-covered-work")
            if let sent = model.decisionFeed.replies[item.id] {
                Label("Sent to \(sent)", systemImage: "checkmark.circle").font(.subheadline.weight(.medium)).foregroundStyle(PhonePalette.muted)
                    .accessibilityIdentifier("decision-reply-sent")
            }
            TextField("Answer \(title)", text: $reply, axis: .vertical)
                .lineLimit(2...6)
                .textFieldStyle(.roundedBorder)
                .disabled(sending)
                .accessibilityIdentifier("decision-reply")
            Button(sending ? "Sending…" : "Send to \(title)") { Task { await sendReply() } }
                .buttonStyle(.borderedProminent)
                .disabled(sending || reply.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("decision-reply-send")
        }
    }

    private var current: DecisionItem {
        var shown = item
        if item.answerable { shown.requests = remaining }
        return shown
    }

    private func dismiss() async {
        failure = await model.dismissDecision(current)
        if failure == nil { done() }
    }

    private func sendReply() async {
        let words = reply.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !words.isEmpty, !sending else { return }
        sending = true
        defer { sending = false }
        failure = await model.replyToParked(item, words: words)
        if failure == nil { reply = "" }
    }
}
