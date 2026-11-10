import SwiftUI

struct QuestionPagerView: View {
    let request: SessionRequest
    let answering: Bool
    let accent: AnyShapeStyle
    let actionTint: AnyShapeStyle?
    let respond: (RequestDecision) -> Void
    let focusOnAppear: Bool
    @State private var pager: QuestionPager
    @FocusState private var focused: Bool
    @FocusState private var typing: PagerTextField?

    init(request: SessionRequest, answering: Bool, accent: AnyShapeStyle, actionTint: AnyShapeStyle? = nil, focusOnAppear: Bool = false, respond: @escaping (RequestDecision) -> Void) {
        self.request = request
        self.answering = answering
        self.accent = accent
        self.actionTint = actionTint
        self.focusOnAppear = focusOnAppear
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
        #if os(macOS)
        .focusable()
        .focusEffectDisabled()
        .focused($focused)
        .onAppear { if focusOnAppear { focused = true } }
        #endif
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
                     systemImage: pager.isAnswered(index) ? "checkmark.circle.fill" : (index < 50 ? "\(index + 1).circle" : "circle"),
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
            .foregroundStyle(selected ? AnyShapeStyle(.primary) : AnyShapeStyle(RoleStyle(.muted)))
            .background(selected ? AnyShapeStyle(.background.opacity(0.6)) : AnyShapeStyle(Color.clear), in: Capsule())
            .overlay(Capsule().strokeBorder(selected ? AnyShapeStyle(.separator) : AnyShapeStyle(.quaternary), lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .focusEffectDisabled()
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityValue(step < pager.reviewStep ? (pager.isAnswered(step) ? "Answered" : "Not answered") : "")
    }

    private func page(_ question: SessionRequest.Question) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            if pager.isSingle, !question.header.isEmpty {
                Text(question.header).font(.caption).foregroundStyle(RoleStyle(.muted))
            }
            Text(question.question).font(.body.weight(.medium))
            if let links = question.links {
                PagerLinks(links: links, accent: accent)
            }
            ForEach(question.options.indices, id: \.self) { index in
                optionRow(question.options[index], index: index, multiSelect: question.multiSelect, suggested: question.suggested == index)
            }
            if pager.takesText {
                if pager.ask {
                    PagerOtherRow(picked: pager.isOtherPicked(at: pager.step), text: typedBinding, disabled: answering,
                                  typing: $typing, choose: { pager.chooseOther() }, submit: advance)
                } else {
                    TextField("Other answer", text: typedBinding)
                        .textFieldStyle(.roundedBorder)
                        .focused($typing, equals: .other)
                        .onSubmit { advance() }
                        .disabled(answering)
                }
            }
            if pager.takesNote, pager.isSingle {
                PagerNoteField(note: $pager.note, disabled: answering, typing: $typing)
            }
        }
    }

    private var typedBinding: Binding<String> {
        Binding(get: { pager.typedAnswer(at: pager.step) }, set: { pager.type($0) })
    }

    private func optionRow(_ option: SessionRequest.Option, index: Int, multiSelect: Bool, suggested: Bool) -> some View {
        let picked = pager.isPicked(index, at: pager.step)
        let number = index + 1
        return Button {
            choose(index)
        } label: {
            HStack(alignment: .firstTextBaseline) {
                #if os(macOS)
                Text(number < 10 ? "\(number)" : "").font(.caption.monospacedDigit()).foregroundStyle(RoleStyle(.faint))
                    .frame(width: 10, alignment: .trailing)
                #endif
                Image(systemName: picked ? (multiSelect ? "checkmark.square.fill" : "largecircle.fill.circle") : (multiSelect ? "square" : "circle"))
                VStack(alignment: .leading, spacing: 2) {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(option.label)
                        if suggested {
                            SuggestedMark()
                        }
                    }
                    if !option.description.isEmpty {
                        Text(option.description).font(.caption).foregroundStyle(RoleStyle(.muted))
                    }
                }
                Spacer(minLength: 0)
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
            if pager.takesNote {
                PagerNoteField(note: $pager.note, disabled: answering, typing: $typing)
            }
        }
    }

    private var actions: some View {
        HStack {
            if pager.ask, pager.onReview || pager.isSingle {
                let ready = !answering && pager.canSubmit
                PagerActionButton(title: pager.isSingle ? "Submit" : "Submit answers", prominent: ready, enabled: ready, tint: actionTint, action: advance)
            } else if pager.ask {
                let answered = pager.isAnswered(pager.step)
                PagerActionButton(title: answered ? "Next" : "Skip", prominent: answered, enabled: !answering, tint: actionTint, action: advance)
            } else if pager.onReview || (pager.isSingle && !pager.isOneTap) {
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
            if !pager.ask {
                Button("Decline", role: .destructive) { respond(.deny) }
                    .disabled(answering)
            }
        }
    }

    private func choose(_ option: Int) {
        focused = true
        if let decision = pager.pick(option) { respond(decision) }
    }

    private func advance() {
        guard !answering else { return }
        if let decision = pager.submit() { respond(decision) }
    }

    private func handle(_ press: KeyPress) -> KeyPress.Result {
        switch press.key {
        case .leftArrow where !pager.isSingle && typing == nil:
            pager.move(by: -1)
        case .rightArrow where !pager.isSingle && typing == nil:
            pager.move(by: 1)
        case .tab where !pager.isSingle:
            pager.move(by: press.modifiers.contains(.shift) ? -1 : 1)
        case .return where !press.modifiers.contains(.shift):
            advance()
        default:
            return .ignored
        }
        return .handled
    }

    private func pickNumber(_ characters: String) -> KeyPress.Result {
        guard !answering, typing == nil, let number = Int(characters), let question = pager.current,
              question.options.indices.contains(number - 1) else { return .ignored }
        choose(number - 1)
        return .handled
    }
}
