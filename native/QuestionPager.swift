import Foundation

struct QuestionPager: Hashable {
    let questions: [SessionRequest.Question]
    // An ended turn's ask is answered by one message, so it never sends on a tap, takes a note, and has nothing to decline.
    let ask: Bool
    let optionsOnly: Bool
    var note = ""
    private(set) var step = 0
    private(set) var picks: [Int: Set<Int>] = [:]
    private(set) var typed: [Int: String] = [:]
    private(set) var other: Set<Int> = []

    init(questions: [SessionRequest.Question], ask: Bool = false, optionsOnly: Bool = false) {
        self.questions = questions
        self.ask = ask
        self.optionsOnly = optionsOnly
    }

    var takesText: Bool { !optionsOnly }
    var takesNote: Bool { ask && !optionsOnly }

    var isOneTap: Bool { !ask && questions.count == 1 && !questions[0].multiSelect }
    var isSingle: Bool { questions.count == 1 }
    var reviewStep: Int { questions.count }
    var onReview: Bool { !isSingle && step == reviewStep }
    var current: SessionRequest.Question? { questions.indices.contains(step) ? questions[step] : nil }

    func isPicked(_ option: Int, at index: Int) -> Bool {
        picks[index]?.contains(option) == true
    }

    func typedAnswer(at index: Int) -> String {
        typed[index] ?? ""
    }

    func answer(at index: Int) -> String? {
        guard questions.indices.contains(index) else { return nil }
        let other = typedAnswer(at: index).trimmingCharacters(in: .whitespacesAndNewlines)
        if !other.isEmpty { return other }
        let options = questions[index].options
        let picked = options.indices.filter { isPicked($0, at: index) }.map { options[$0].label }
        return picked.isEmpty ? nil : picked.joined(separator: ", ")
    }

    func isAnswered(_ index: Int) -> Bool { answer(at: index) != nil }

    var canSubmit: Bool { questions.indices.allSatisfy(isAnswered) }

    var decision: RequestDecision? {
        var choices: [String] = []
        var typedAnswers: [String: String] = [:]
        for (index, question) in questions.enumerated() {
            let text = takesText ? typedAnswer(at: index).trimmingCharacters(in: .whitespacesAndNewlines) : ""
            if !text.isEmpty {
                typedAnswers[question.question] = text
                continue
            }
            let picked = question.options.indices.filter { isPicked($0, at: index) }.map { question.options[$0].id }
            guard !picked.isEmpty else { return nil }
            choices += picked
        }
        let note = note.trimmingCharacters(in: .whitespacesAndNewlines)
        return RequestDecision(choices: choices, answers: typedAnswers.isEmpty ? nil : typedAnswers, message: takesNote && !note.isEmpty ? note : nil)
    }

    mutating func choose(_ option: Int) -> Bool {
        guard let question = current, question.options.indices.contains(option) else { return false }
        var picked = picks[step] ?? []
        if question.multiSelect {
            if picked.contains(option) { picked.remove(option) } else { picked.insert(option) }
            picks[step] = picked
            return false
        }
        picks[step] = [option]
        typed[step] = nil
        other.remove(step)
        if isOneTap { return true }
        if !isSingle { step += 1 }
        return false
    }

    func isOtherPicked(at index: Int) -> Bool { other.contains(index) }

    mutating func chooseOther() {
        guard takesText, questions.indices.contains(step) else { return }
        picks[step] = []
        other.insert(step)
    }

    mutating func pick(_ option: Int) -> RequestDecision? {
        choose(option) ? decision : nil
    }

    mutating func submit() -> RequestDecision? {
        next() ? decision : nil
    }

    mutating func type(_ text: String) {
        guard takesText, questions.indices.contains(step) else { return }
        typed[step] = text.isEmpty ? nil : text
    }

    mutating func next() -> Bool {
        if isSingle || onReview { return decision != nil }
        step = min(step + 1, reviewStep)
        return false
    }

    mutating func go(to step: Int) {
        guard !isSingle else { return }
        self.step = max(0, min(step, reviewStep))
    }

    mutating func move(by offset: Int) {
        go(to: step + offset)
    }
}
