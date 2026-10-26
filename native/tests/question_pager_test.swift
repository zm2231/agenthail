@testable import AgenthailHelpers
import Foundation

@main
struct QuestionPagerTest {
    @MainActor
    static func main() {
        let options = [SessionRequest.Option(label: "Yes", description: ""), SessionRequest.Option(label: "No", description: "")]
        let scope = SessionRequest.Question(question: "Which scope?", header: "Scope", multiSelect: false, options: options)
        let targets = SessionRequest.Question(question: "Which targets?", header: "Targets", multiSelect: true,
                                              options: [SessionRequest.Option(label: "Mac", description: ""), SessionRequest.Option(label: "iPhone", description: ""), SessionRequest.Option(label: "Web", description: "")])

        var oneTap = QuestionPager(questions: [scope])
        check(oneTap.isOneTap && oneTap.isSingle && !oneTap.onReview, "one single-select question is one tap")
        check(oneTap.choose(1) == ["Which scope?": "No"], "a pick answers a one-tap question at once")
        oneTap.type("  Maybe  ")
        check(oneTap.next() == ["Which scope?": "Maybe"], "a typed answer to a one-tap question sends trimmed")
        oneTap.go(to: 1)
        check(oneTap.step == 0, "a single question has no other step")

        var single = QuestionPager(questions: [targets])
        check(!single.isOneTap && single.isSingle, "one multi-select question has no chips")
        check(single.next() == nil, "an unanswered single question does not send")
        check(single.choose(2) == nil && single.choose(0) == nil && single.choose(7) == nil, "a toggle never sends")
        check(single.next() == ["Which targets?": "Mac, Web"], "multi-select answers keep option order: \(String(describing: single.answers))")
        _ = single.choose(0)
        check(single.answers == ["Which targets?": "Web"], "a second toggle clears the pick")

        let same = SessionRequest.Question(question: "Which copies?", header: "", multiSelect: true,
                                           options: [SessionRequest.Option(label: "same", description: "first"), SessionRequest.Option(label: "same", description: "second")])
        var twins = QuestionPager(questions: [same])
        _ = twins.choose(0)
        _ = twins.choose(1)
        check(twins.next() == ["Which copies?": "same, same"], "options with the same label are picked apart: \(String(describing: twins.answers))")
        _ = twins.choose(0)
        check(twins.answers == ["Which copies?": "same"] && twins.isPicked(1, at: 0) && !twins.isPicked(0, at: 0), "unpicking one leaves its twin")

        var pager = QuestionPager(questions: [scope, targets])
        check(!pager.isSingle && pager.reviewStep == 2 && pager.step == 0, "two questions start on the first page")
        check(pager.choose(0) == nil && pager.step == 1, "a single-select pick advances")
        check(pager.isAnswered(0) && !pager.isAnswered(1) && !pager.canSubmit, "only the first is answered")
        _ = pager.choose(1)
        check(pager.step == 1, "a multi-select toggle stays on its page")
        check(pager.next() == nil && pager.onReview, "next moves to the review page")
        pager.go(to: 0)
        check(pager.step == 0 && pager.isPicked(0, at: 0), "going back keeps the answer")
        _ = pager.choose(1)
        check(pager.answer(at: 0) == "No" && pager.step == 1, "changing an answer replaces it and advances")
        pager.move(by: 5)
        check(pager.onReview, "moving clamps at the review page")
        pager.move(by: -9)
        check(pager.step == 0, "moving clamps at the first page")
        pager.type("Both")
        check(pager.answer(at: 0) == "Both", "a typed answer wins over a pick without pressing next")
        pager.type("")
        check(pager.answer(at: 0) == "No", "clearing the typed answer brings the pick back")
        pager.go(to: 2)
        check(pager.next() == ["Which scope?": "No", "Which targets?": "iPhone"], "review sends every answer")

        var gated = QuestionPager(questions: [scope, targets])
        gated.go(to: 1)
        gated.type("All of them")
        gated.go(to: 2)
        check(!gated.canSubmit && gated.next() == nil && gated.onReview, "review does not send while a question is unanswered")
        gated.go(to: 0)
        _ = gated.choose(0)
        check(gated.canSubmit && gated.answers == ["Which scope?": "Yes", "Which targets?": "All of them"], "typed counts as answered")

        let request = SessionRequest(id: "r1", kind: "question", tool: "AskUserQuestion", questions: [scope, targets])
        let drafts = QuestionPagerDrafts(limit: 2)
        var draft = drafts.pager(for: request)
        _ = draft.choose(0)
        drafts.save(draft, for: "r1")
        check(drafts.pager(for: request).step == 1, "a saved draft comes back for the same request")
        let other = SessionRequest(id: "r2", kind: "question", tool: "AskUserQuestion", questions: [scope, targets])
        check(drafts.pager(for: other).step == 0, "another request starts at the first page")
        drafts.save(drafts.pager(for: other), for: "r2")
        drafts.save(QuestionPager(questions: [scope]), for: "r3")
        check(drafts.pager(for: request).step == 0, "the oldest draft goes past the limit")
        let changed = SessionRequest(id: "r3", kind: "question", tool: "AskUserQuestion", questions: [targets])
        check(drafts.pager(for: changed).questions == [targets], "a draft for different questions is not reused")
        print("question pager ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fatalError(message) }
    }
}
