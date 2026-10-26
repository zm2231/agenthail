import SwiftUI

struct QuestionPagerView: View {
    let request: SessionRequest
    let answering: Bool
    let accent: AnyShapeStyle
    let respond: (RequestDecision) -> Void
    @State private var pager: QuestionPager
    @FocusState private var focused: Bool

    init(request: SessionRequest, answering: Bool, accent: AnyShapeStyle, respond: @escaping (RequestDecision) -> Void) {
        self.request = request
        self.answering = answering
        self.accent = accent
        self.respond = respond
        _pager = State(initialValue: QuestionPagerDrafts.shared.pager(for: request))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if !pager.isSingle {
                chips
            }
            if pager.onReview {
                review
            } else if let question = pager.current {
                page(question)
            }
            actions
        }
        .contentShape(Rectangle())
        .onTapGesture { focused = true }
        .focusable()
        .focusEffectDisabled()
        .focused($focused)
        .onKeyPress(keys: [.leftArrow, .rightArrow, .tab, .return]) { press in
            handle(press)
        }
        .onKeyPress(characters: .decimalDigits) { press in
            pickNumber(press.characters)
        }
        .onChange(of: pager) { _, pager in
            QuestionPagerDrafts.shared.save(pager, for: request.id)
        }
    }

    private var chips: some View {
        HStack(spacing: 6) {
            ForEach(pager.questions.indices, id: \.self) { index in
                chip(pager.questions[index].header.isEmpty ? "Question \(index + 1)" : pager.questions[index].header,
                     systemImage: pager.isAnswered(index) ? "checkmark.square.fill" : "square",
                     step: index)
            }
            chip("Submit", systemImage: "paperplane", step: pager.reviewStep)
                .layoutPriority(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityIdentifier("question-pager-chips")
    }

    private func chip(_ title: String, systemImage: String, step: Int) -> some View {
        let selected = pager.step == step
        return Button {
            pager.go(to: step)
            focused = true
        } label: {
            Label {
                Text(title).lineLimit(1).truncationMode(.tail)
            } icon: {
                Image(systemName: systemImage)
            }
            .font(.caption.weight(selected ? .semibold : .regular))
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .foregroundStyle(selected ? accent : AnyShapeStyle(RoleStyle(.muted)))
            .background(selected ? AnyShapeStyle(.quaternary) : AnyShapeStyle(Color.clear), in: Capsule())
            .overlay(Capsule().strokeBorder(.quaternary))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityValue(step < pager.reviewStep ? (pager.isAnswered(step) ? "Answered" : "Not answered") : "")
    }

    private func page(_ question: SessionRequest.Question) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if pager.isSingle, !question.header.isEmpty {
                Text(question.header).font(.caption).foregroundStyle(RoleStyle(.muted))
            }
            Text(question.question).font(.body.weight(.medium))
            ForEach(Array(question.options.enumerated()), id: \.element.label) { number, option in
                optionRow(option, number: number + 1, multiSelect: question.multiSelect)
            }
            TextField("Other answer", text: Binding(get: { pager.typedAnswer(at: pager.step) }, set: { pager.type($0) }))
                .textFieldStyle(.roundedBorder)
                .onSubmit { advance() }
                .disabled(answering)
        }
    }

    private func optionRow(_ option: SessionRequest.Option, number: Int, multiSelect: Bool) -> some View {
        let picked = pager.isPicked(option.label, at: pager.step)
        return Button {
            choose(option.label)
        } label: {
            HStack(alignment: .firstTextBaseline) {
                Image(systemName: picked ? (multiSelect ? "checkmark.square.fill" : "largecircle.fill.circle") : (multiSelect ? "square" : "circle"))
                VStack(alignment: .leading, spacing: 2) {
                    Text(option.label)
                    if !option.description.isEmpty {
                        Text(option.description).font(.caption).foregroundStyle(RoleStyle(.muted))
                    }
                }
                Spacer(minLength: 0)
                #if os(macOS)
                if number < 10 {
                    Text("\(number)").font(.caption.monospacedDigit()).foregroundStyle(RoleStyle(.faint))
                }
                #endif
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(answering)
    }

    private var review: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Review your answers").font(.body.weight(.medium))
            ForEach(pager.questions.indices, id: \.self) { index in
                let question = pager.questions[index]
                Button {
                    pager.go(to: index)
                    focused = true
                } label: {
                    HStack(alignment: .firstTextBaseline) {
                        Image(systemName: pager.isAnswered(index) ? "checkmark.square.fill" : "exclamationmark.square")
                            .foregroundStyle(pager.isAnswered(index) ? AnyShapeStyle(RoleStyle(.muted)) : AnyShapeStyle(RoleStyle(.needsYou)))
                        VStack(alignment: .leading, spacing: 2) {
                            Text(question.header.isEmpty ? question.question : question.header)
                                .font(.caption).foregroundStyle(RoleStyle(.muted))
                            if let answer = pager.answer(at: index) {
                                Text(answer)
                            } else {
                                Text("Not answered").foregroundStyle(RoleStyle(.needsYou))
                            }
                        }
                        Spacer(minLength: 0)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
            if !pager.canSubmit {
                Text("Answer every question to submit.").font(.caption).foregroundStyle(RoleStyle(.muted))
            }
        }
    }

    private var actions: some View {
        HStack {
            if pager.onReview || (pager.isSingle && !pager.isOneTap) {
                Button(pager.isSingle ? "Submit" : "Submit answers") { advance() }
                    .buttonStyle(.borderedProminent)
                    .disabled(answering || !pager.canSubmit)
            } else if pager.isOneTap {
                if pager.canSubmit {
                    Button("Answer") { advance() }
                        .buttonStyle(.borderedProminent)
                        .disabled(answering)
                }
            } else {
                Button("Next") { advance() }
                    .buttonStyle(.bordered)
                    .disabled(answering)
            }
            Spacer()
            Button("Decline", role: .destructive) { respond(.deny) }
                .disabled(answering)
        }
    }

    private func choose(_ label: String) {
        focused = true
        if let answers = pager.choose(label) { respond(.answers(answers)) }
    }

    private func advance() {
        guard !answering else { return }
        if let answers = pager.next() { respond(.answers(answers)) }
    }

    private func handle(_ press: KeyPress) -> KeyPress.Result {
        switch press.key {
        case .leftArrow where !pager.isSingle:
            pager.move(by: -1)
        case .rightArrow where !pager.isSingle:
            pager.move(by: 1)
        case .tab where !pager.isSingle:
            pager.move(by: press.modifiers.contains(.shift) ? -1 : 1)
        case .return:
            advance()
        default:
            return .ignored
        }
        return .handled
    }

    private func pickNumber(_ characters: String) -> KeyPress.Result {
        guard !answering, let number = Int(characters), let question = pager.current,
              question.options.indices.contains(number - 1) else { return .ignored }
        choose(question.options[number - 1].label)
        return .handled
    }
}
