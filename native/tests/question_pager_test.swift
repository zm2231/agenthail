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
        check(oneTap.choose("No") == ["Which scope?": "No"], "a pick answers a one-tap question at once")
        oneTap.type("  Maybe  ")
        check(oneTap.next() == ["Which scope?": "Maybe"], "a typed answer to a one-tap question sends trimmed")
        oneTap.go(to: 1)
        check(oneTap.step == 0, "a single question has no other step")

        var single = QuestionPager(questions: [targets])
        check(!single.isOneTap && single.isSingle, "one multi-select question has no chips")
        check(single.next() == nil, "an unanswered single question does not send")
        check(single.choose("Web") == nil && single.choose("Mac") == nil, "a toggle never sends")
        check(single.next() == ["Which targets?": "Mac, Web"], "multi-select answers keep option order: \(String(describing: single.answers))")
        _ = single.choose("Mac")
        check(single.answers == ["Which targets?": "Web"], "a second toggle clears the pick")

        var pager = QuestionPager(questions: [scope, targets])
        check(!pager.isSingle && pager.reviewStep == 2 && pager.step == 0, "two questions start on the first page")
        check(pager.choose("Yes") == nil && pager.step == 1, "a single-select pick advances")
        check(pager.isAnswered(0) && !pager.isAnswered(1) && !pager.canSubmit, "only the first is answered")
        _ = pager.choose("iPhone")
        check(pager.step == 1, "a multi-select toggle stays on its page")
        check(pager.next() == nil && pager.onReview, "next moves to the review page")
        pager.go(to: 0)
        check(pager.step == 0 && pager.isPicked("Yes", at: 0), "going back keeps the answer")
        _ = pager.choose("No")
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
        _ = gated.choose("Yes")
        check(gated.canSubmit && gated.answers == ["Which scope?": "Yes", "Which targets?": "All of them"], "typed counts as answered")

        let request = SessionRequest(id: "r1", kind: "question", tool: "AskUserQuestion", questions: [scope, targets])
        let drafts = QuestionPagerDrafts(limit: 2)
        var draft = drafts.pager(for: request)
        _ = draft.choose("Yes")
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
