import SwiftUI

struct RequestCard: View {
    let request: SessionRequest
    let answering: Bool
    let accent: Color
    let respond: (RequestDecision) -> Void
    @State private var picks: [Int: Set<String>] = [:]
    @State private var typed: [Int: String] = [:]

    private var questions: [SessionRequest.Question] { request.questions ?? [] }
    private var singleChoice: Bool { questions.count == 1 && !questions[0].multiSelect }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
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
        .padding(12)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("request-\(request.id)")
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
