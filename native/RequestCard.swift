import SwiftUI

struct RequestCard: View {
    let request: SessionRequest
    let answering: Bool
    let accent: AnyShapeStyle
    let respond: (RequestDecision) -> Void
    /// Who leads the project now, for a request from a lead a restart replaced.
    var leadNow: String? = nil
    var withdraw: (() -> Void)? = nil
    var asker: String = "The agent"
    var attention = AnyShapeStyle(RoleStyle(.needsYou))
    var surface: AnyShapeStyle? = AnyShapeStyle(.regularMaterial)
    var actionTint: AnyShapeStyle? = nil
    var focusesPager = false
    var agentSurface = ""
    @State private var form = ElicitationForm()
    @State private var replying = false
    @State private var reply = ""

    var heading: String { Self.timed(Self.heading(request, asker: asker, surface: agentSurface), request) }

    static func timed(_ heading: String, _ request: SessionRequest) -> String {
        TurnClock.time(request.createdAt).map { "\(heading) · \($0)" } ?? heading
    }

    static func heading(_ request: SessionRequest, asker: String, surface: String = "") -> String {
        if request.isAsk { return "\(asker) asks: \(request.title ?? "")" }
        let agent = surface.isEmpty ? "The agent" : SessionRefinement.surfaceLabel(surface)
        return request.isQuestion ? "\(agent) asks" : "\(agent) wants your approval"
    }

    static let denyingChoices: Set<String> = ["deny", "never", "stop"]

    static func approvalButtons(_ actions: [SessionRequest.Action]) -> (allowing: [SessionRequest.Action], denying: [SessionRequest.Action], reply: SessionRequest.Action?) {
        let buttons = actions.filter { $0.id != RequestNotifications.replyChoice }
        return (buttons.filter { !denyingChoices.contains($0.id) }, buttons.filter { denyingChoices.contains($0.id) }, actions.first { $0.id == RequestNotifications.replyChoice })
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if let leadNow, let withdraw {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text("From a replaced lead. \(leadNow) leads this project now, so this request no longer needs you.")
                        .font(.footnote).foregroundStyle(RoleStyle(.muted))
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Button("Withdraw", action: withdraw).disabled(answering)
                }
            }
            if request.kind == "input", let elicitation = request.elicitation {
                inputView(elicitation)
            } else {
                Label(heading, systemImage: request.usesPager ? "questionmark.bubble" : "hand.raised")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(request.isAsk ? attention : accent)
                if request.usesPager {
                    QuestionPagerView(request: request, answering: answering, accent: accent, actionTint: actionTint, focusOnAppear: focusesPager, respond: respond)
                } else {
                    Text(request.summary).font(.body.weight(.medium))
                    if let description = request.description, !description.isEmpty {
                        Text(description).font(.footnote).foregroundStyle(RoleStyle(.muted))
                    }
                    if let reason = request.reason, !reason.isEmpty {
                        Text(reason).font(.footnote).foregroundStyle(RoleStyle(.muted))
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityIdentifier("request-reason")
                    }
                    if let input = request.input, !input.isEmpty, input != "{}" {
                        Text(input).font(.caption.monospaced()).lineLimit(6).textSelection(.enabled)
                            .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
                    }
                    if let path = request.blockedPath, !path.isEmpty {
                        Text(path).font(.caption.monospaced()).foregroundStyle(RoleStyle(.muted))
                    }
                    approvalView
                }
            }
        }
        .padding(12)
        .background { if let surface { RoundedRectangle(cornerRadius: 12).fill(surface) } }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("request-\(request.id)")
    }

    static let noChoicesNote = "This computer sent no choices for this request, so answer it where the session runs. Updating Agenthail there lets you answer it here."

    @ViewBuilder
    private var approvalView: some View {
        let buttons = Self.approvalButtons(request.actions)
        if request.actions.isEmpty {
            Text(Self.noChoicesNote).font(.footnote).foregroundStyle(RoleStyle(.muted))
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("request-no-choices")
        } else {
            approvalButtonsView(buttons)
        }
    }

    @ViewBuilder
    private func approvalButtonsView(_ buttons: (allowing: [SessionRequest.Action], denying: [SessionRequest.Action], reply: SessionRequest.Action?)) -> some View {
        VStack(alignment: .trailing, spacing: 8) {
            HStack {
                ForEach(buttons.allowing, id: \.id) { action in
                    if action.id == "allow" {
                        Button(action.title) { send(.choice(action.id)) }.buttonStyle(.borderedProminent)
                    } else {
                        Button(action.title) { send(.choice(action.id)) }.buttonStyle(.bordered)
                    }
                }
                Spacer()
            }
            HStack {
                Spacer()
                if let action = buttons.reply, !replying {
                    Button(action.title) { replying = true }.buttonStyle(.bordered)
                }
                ForEach(buttons.denying, id: \.id) { action in
                    Button(action.title, role: .destructive) { send(.choice(action.id)) }.buttonStyle(.bordered)
                }
            }
            if replying, buttons.reply != nil {
                HStack {
                    TextField("Message", text: $reply).textFieldStyle(.roundedBorder)
                    Button("Send", role: .destructive) { send(RequestDecision(choice: RequestNotifications.replyChoice, message: reply)) }
                        .buttonStyle(.bordered)
                        .disabled(reply.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
        .disabled(answering)
    }

    @ViewBuilder
    private func inputView(_ elicitation: Elicitation) -> some View {
        Label(Self.timed("\(elicitation.server.isEmpty ? "An MCP server" : elicitation.server) asks for input", request), systemImage: "square.and.pencil")
            .font(.caption.weight(.semibold))
            .foregroundStyle(accent)
        Text(elicitation.message).font(.body.weight(.medium))
        if elicitation.mode == "url" {
            if let url = elicitation.url.flatMap(URL.init(string:)), ["http", "https"].contains(url.scheme?.lowercased()) {
                Link(url.absoluteString, destination: url).font(.footnote)
            } else {
                Text(elicitation.url ?? "").font(.footnote.monospaced()).textSelection(.enabled)
            }
            HStack {
                Button("Opened, continue") { send(.allow) }
                    .buttonStyle(.borderedProminent)
                Spacer()
                Button("Decline", role: .destructive) { send(.deny) }
            }
            .disabled(answering)
        } else {
            let fields = elicitation.fields ?? []
            ForEach(fields, id: \.name) { field in
                fieldView(field)
            }
            HStack {
                Button("Submit") {
                    if let content = form.content(fields) { send(.content(content)) }
                }
                .buttonStyle(.borderedProminent)
                .disabled(answering || form.content(fields) == nil)
                Spacer()
                Button("Decline", role: .destructive) { send(.deny) }
                    .disabled(answering)
            }
        }
    }

    @ViewBuilder
    private func fieldView(_ field: Elicitation.Field) -> some View {
        let label = field.label + (field.required ? " *" : "")
        VStack(alignment: .leading, spacing: 4) {
            if field.multiSelect {
                Text(label).font(.caption).foregroundStyle(RoleStyle(.muted))
                ForEach(field.options ?? [], id: \.value) { option in
                    Toggle(option.label, isOn: Binding(get: { form.picked(field).contains(option.value) }, set: { form.pick(option.value, in: field, $0) }))
                }
            } else if let options = field.options {
                Picker(label, selection: Binding(get: { form.text(field) }, set: { form.texts[field.name] = $0 })) {
                    Text("None").tag("")
                    ForEach(options, id: \.value) { option in Text(option.label).tag(option.value) }
                }
            } else if field.type == "boolean" {
                Toggle(label, isOn: Binding(get: { form.isOn(field) }, set: { form.on[field.name] = $0 }))
            } else {
                TextField(label, text: Binding(get: { form.text(field) }, set: { form.texts[field.name] = $0 }))
                    .textFieldStyle(.roundedBorder)
            }
            if !field.description.isEmpty {
                Text(field.description).font(.caption).foregroundStyle(RoleStyle(.muted))
            }
        }
        .disabled(answering)
    }

    private func send(_ decision: RequestDecision) {
        respond(decision)
    }
}

/// What a person entered in an MCP server's form, read with each field's
/// default until they change it.
struct ElicitationForm: Hashable {
    var texts: [String: String] = [:]
    var on: [String: Bool] = [:]
    var picks: [String: Set<String>] = [:]

    func text(_ field: Elicitation.Field) -> String {
        if let text = texts[field.name] { return text }
        switch field.initial {
        case .string(let value): return value
        case .number(let value): return value.rounded() == value && abs(value) < 1e15 ? String(Int64(value)) : String(value)
        default: return ""
        }
    }

    func isOn(_ field: Elicitation.Field) -> Bool {
        if let on = on[field.name] { return on }
        if case .bool(let value) = field.initial { return value }
        return false
    }

    func picked(_ field: Elicitation.Field) -> Set<String> {
        if let picked = picks[field.name] { return picked }
        if case .strings(let values) = field.initial { return Set(values) }
        return []
    }

    mutating func pick(_ value: String, in field: Elicitation.Field, _ chosen: Bool) {
        var picked = picked(field)
        if chosen { picked.insert(value) } else { picked.remove(value) }
        picks[field.name] = picked
    }

    /// The form's values as the server takes them, or nil while a required
    /// field is empty or a number field holds something else. An empty
    /// optional field is left out.
    func content(_ fields: [Elicitation.Field]) -> [String: ElicitationValue]? {
        var content: [String: ElicitationValue] = [:]
        for field in fields {
            if field.multiSelect {
                let values = (field.options ?? []).map(\.value).filter(picked(field).contains)
                if field.required && values.isEmpty { return nil }
                content[field.name] = .strings(values)
                continue
            }
            if field.type == "boolean" && field.options == nil {
                content[field.name] = .bool(isOn(field))
                continue
            }
            let text = text(field).trimmingCharacters(in: .whitespacesAndNewlines)
            if text.isEmpty {
                if field.required { return nil }
                continue
            }
            switch field.type {
            case "integer":
                guard let value = Int64(text) else { return nil }
                content[field.name] = .number(Double(value))
            case "number":
                guard let value = Double(text), value.isFinite else { return nil }
                content[field.name] = .number(value)
            default:
                content[field.name] = .string(text)
            }
        }
        return content
    }
}

/// A session waiting on a prompt Agenthail cannot answer, with where to
/// answer it, or how to clear one nothing shows.
struct ElsewhereWaitCard: View {
    let waiting: WaitingState
    let place: WaitingPlace
    let surface: String
    let hostName: String?
    let accent: AnyShapeStyle
    let open: (() -> Void)?
    var restart: (() -> Void)? = nil

    /// Whether a session's wait is shown as this card rather than as a request
    /// to answer: any wait Agenthail does not hold, such as a dialog, alongside
    /// which a request still listed is stale.
    static func shows(_ waiting: WaitingState?, readOnly: Bool, respond: Bool) -> Bool {
        guard let waiting else { return false }
        return waiting.answerable != true || readOnly || !respond
    }

    static func heading(kind: String, surface: String, place: WaitingPlace = .elsewhere) -> String {
        let agent = SessionRefinement.surfaceLabel(surface)
        if place == .restart { return "\(agent) is waiting on a prompt Agenthail can't reach" }
        switch kind {
        case "question": return "\(agent) asks you a question"
        case "input": return "\(agent) needs your input"
        case "dialog": return "\(agent) has a dialog open in its terminal"
        default: return "\(agent) needs your approval"
        }
    }

    static func detail(place: WaitingPlace, hostName: String?) -> String {
        switch place {
        case .restart:
            return "Nothing shows this prompt, so no one can answer it. Stop and resume the session to clear it; messages you send wait until then."
        case .terminal:
            return "Agenthail can't answer this one. Answer it in the session's terminal\(hostName.map { ", in \($0)" } ?? ""). Messages you send wait until then."
        default:
            return "Agenthail can't answer this one. Answer it where the session runs\(hostName.map { ", in \($0)" } ?? ", such as its terminal"). Messages you send wait until then."
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label(Self.heading(kind: waiting.kind, surface: surface, place: place), systemImage: place == .restart ? "exclamationmark.bubble" : waiting.kind == "approval" ? "hand.raised" : waiting.kind == "dialog" ? "macwindow" : "questionmark.bubble")
                .font(.caption.weight(.semibold))
                .foregroundStyle(accent)
            Text(Self.detail(place: place, hostName: hostName))
                .font(.footnote)
                .foregroundStyle(RoleStyle(.muted))
            HStack {
                if place == .restart, let restart {
                    Button("Stop and resume", systemImage: "arrow.clockwise", action: restart)
                        .buttonStyle(.borderedProminent)
                }
                if let open {
                    if place == .restart, restart != nil {
                        Button("Open in \(hostName ?? "terminal")", systemImage: "arrow.up.forward.app", action: open)
                            .buttonStyle(.bordered)
                    } else {
                        Button("Open in \(hostName ?? "terminal")", systemImage: "arrow.up.forward.app", action: open)
                            .buttonStyle(.borderedProminent)
                    }
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("waiting-elsewhere-\(waiting.id)")
    }
}
