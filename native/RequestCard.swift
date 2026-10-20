import SwiftUI

struct RequestCard: View {
    let request: SessionRequest
    let answering: Bool
    let accent: Color
    let respond: (RequestDecision) -> Void
    @State private var picks: [Int: Set<String>] = [:]
    @State private var typed: [Int: String] = [:]
    @State private var form = ElicitationForm()

    private var questions: [SessionRequest.Question] { request.questions ?? [] }
    private var singleChoice: Bool { questions.count == 1 && !questions[0].multiSelect }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if request.kind == "input", let elicitation = request.elicitation {
                inputView(elicitation)
            } else {
                Label(request.isQuestion ? "Claude asks" : "Claude wants your approval", systemImage: request.isQuestion ? "questionmark.bubble" : "hand.raised")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(accent)
                if request.isQuestion {
                    ForEach(Array(questions.enumerated()), id: \.offset) { index, question in
                        questionView(index, question)
                    }
                    HStack {
                        if !singleChoice || typed[0]?.isEmpty == false {
                            Button("Answer") { submitAnswers() }
                                .buttonStyle(.borderedProminent)
                                .disabled(answering || answers() == nil)
                        }
                        Spacer()
                        Button("Dismiss", role: .destructive) { send(.deny) }
                            .disabled(answering)
                    }
                } else {
                    Text(request.summary).font(.body.weight(.medium))
                    if let description = request.description, !description.isEmpty {
                        Text(description).font(.footnote).foregroundStyle(.secondary)
                    }
                    if let input = request.input, !input.isEmpty, input != "{}" {
                        Text(input).font(.caption.monospaced()).lineLimit(6).textSelection(.enabled)
                            .padding(8).frame(maxWidth: .infinity, alignment: .leading)
                            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
                    }
                    if let path = request.blockedPath, !path.isEmpty {
                        Text(path).font(.caption.monospaced()).foregroundStyle(.secondary)
                    }
                    HStack {
                        Button("Allow") { send(.allow) }
                            .buttonStyle(.borderedProminent)
                        if request.alwaysAllow {
                            Button("Always allow") { send(.alwaysAllow) }
                                .buttonStyle(.bordered)
                        }
                        Spacer()
                        Button("Deny", role: .destructive) { send(.deny) }
                            .buttonStyle(.bordered)
                    }
                    .disabled(answering)
                }
            }
        }
        .padding(12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("request-\(request.id)")
    }

    @ViewBuilder
    private func inputView(_ elicitation: Elicitation) -> some View {
        Label("\(elicitation.server.isEmpty ? "An MCP server" : elicitation.server) asks for input", systemImage: "square.and.pencil")
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
                Text(label).font(.caption).foregroundStyle(.secondary)
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
                Text(field.description).font(.caption).foregroundStyle(.secondary)
            }
        }
        .disabled(answering)
    }

    @ViewBuilder
    private func questionView(_ index: Int, _ question: SessionRequest.Question) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if !question.header.isEmpty {
                Text(question.header).font(.caption).foregroundStyle(.secondary)
            }
            Text(question.question).font(.body.weight(.medium))
            ForEach(question.options, id: \.label) { option in
                Button {
                    choose(option.label, index: index, question: question)
                } label: {
                    HStack(alignment: .firstTextBaseline) {
                        Image(systemName: picks[index]?.contains(option.label) == true ? (question.multiSelect ? "checkmark.square.fill" : "largecircle.fill.circle") : (question.multiSelect ? "square" : "circle"))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(option.label)
                            if !option.description.isEmpty {
                                Text(option.description).font(.caption).foregroundStyle(.secondary)
                            }
                        }
                        Spacer(minLength: 0)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(answering)
            }
            TextField("Other answer", text: Binding(get: { typed[index] ?? "" }, set: { typed[index] = $0 }))
                .textFieldStyle(.roundedBorder)
        }
    }

    private func choose(_ label: String, index: Int, question: SessionRequest.Question) {
        if singleChoice {
            send(.answers([question.question: label]))
            return
        }
        var picked = picks[index] ?? []
        if question.multiSelect {
            if picked.contains(label) { picked.remove(label) } else { picked.insert(label) }
        } else {
            picked = [label]
        }
        picks[index] = picked
    }

    private func answers() -> [String: String]? {
        var answers: [String: String] = [:]
        for (index, question) in questions.enumerated() {
            let other = (typed[index] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            let picked = question.options.map(\.label).filter { picks[index]?.contains($0) == true }
            let answer = other.isEmpty ? picked.joined(separator: ", ") : other
            guard !answer.isEmpty else { return nil }
            answers[question.question] = answer
        }
        return answers
    }

    private func submitAnswers() {
        guard let answers = answers() else { return }
        send(.answers(answers))
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

/// A session waiting on a prompt Agenthail cannot answer, with where to answer it.
struct ElsewhereWaitCard: View {
    let waiting: WaitingState
    let surface: String
    let hostName: String?
    let accent: Color
    let open: (() -> Void)?

    /// Whether a session's wait is shown as this card rather than as a request
    /// to answer. Claude reports a dialog only when nothing else waits, so a
    /// request still listed alongside one is stale.
    static func shows(_ waiting: WaitingState?, readOnly: Bool, respond: Bool) -> Bool {
        guard let waiting else { return false }
        return waiting.kind == "dialog" || readOnly || !respond
    }

    static func heading(kind: String, surface: String) -> String {
        let agent = SessionRefinement.surfaceLabel(surface)
        switch kind {
        case "question": return "\(agent) asks you a question"
        case "input": return "\(agent) needs your input"
        case "dialog": return "\(agent) has a dialog open in its terminal"
        default: return "\(agent) needs your approval"
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label(Self.heading(kind: waiting.kind, surface: surface), systemImage: waiting.kind == "approval" ? "hand.raised" : waiting.kind == "dialog" ? "macwindow" : "questionmark.bubble")
                .font(.caption.weight(.semibold))
                .foregroundStyle(accent)
            Text("Agenthail can't answer this one. Answer it where the session runs\(hostName.map { ", in \($0)" } ?? ", such as its terminal"). Messages you send wait until then.")
                .font(.footnote)
                .foregroundStyle(.secondary)
            if let open {
                Button("Open in \(hostName ?? "terminal")", systemImage: "arrow.up.forward.app", action: open)
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("waiting-elsewhere-\(waiting.id)")
    }
}
