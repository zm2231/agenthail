import Foundation

struct QuestionPager: Hashable {
    let questions: [SessionRequest.Question]
    // An ended turn's ask is answered by one message, so it never sends on a tap, takes a note, and has nothing to decline.
    let ask: Bool
    var note = ""
    private(set) var step = 0
    private(set) var picks: [Int: Set<Int>] = [:]
    private(set) var typed: [Int: String] = [:]
    private(set) var other: Set<Int> = []

    init(questions: [SessionRequest.Question], ask: Bool = false) {
        self.questions = questions
        self.ask = ask
    }

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

    var answers: [String: String]? {
        var answers: [String: String] = [:]
        for index in questions.indices {
            guard let answer = answer(at: index) else { return nil }
            answers[questions[index].question] = answer
        }
        return answers
    }

    mutating func choose(_ option: Int) -> [String: String]? {
        guard let question = current, question.options.indices.contains(option) else { return nil }
        if isOneTap { return [question.question: question.options[option].label] }
        var picked = picks[step] ?? []
        if question.multiSelect {
            if picked.contains(option) { picked.remove(option) } else { picked.insert(option) }
            picks[step] = picked
            return nil
        }
        picks[step] = [option]
        typed[step] = nil
        other.remove(step)
        if !isSingle { step += 1 }
        return nil
    }

    func isOtherPicked(at index: Int) -> Bool { other.contains(index) }

    mutating func chooseOther() {
        guard questions.indices.contains(step) else { return }
        picks[step] = []
        other.insert(step)
    }

    mutating func pick(_ option: Int) -> RequestDecision? {
        choose(option).map(decision)
    }

    mutating func submit() -> RequestDecision? {
        next().map(decision)
    }

    func decision(_ answers: [String: String]) -> RequestDecision {
        let note = note.trimmingCharacters(in: .whitespacesAndNewlines)
        return RequestDecision(behavior: "allow", message: ask && !note.isEmpty ? note : nil, answers: answers)
    }

    mutating func type(_ text: String) {
        guard questions.indices.contains(step) else { return }
        typed[step] = text.isEmpty ? nil : text
    }

    mutating func next() -> [String: String]? {
        if isSingle || onReview { return answers }
        step = min(step + 1, reviewStep)
        return nil
    }

    mutating func go(to step: Int) {
        guard !isSingle else { return }
        self.step = max(0, min(step, reviewStep))
    }

    mutating func move(by offset: Int) {
        go(to: step + offset)
    }
}
