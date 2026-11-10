@testable import AgenthailHelpers
import Foundation

@main
struct QuestionPagerTest {
    @MainActor
    static func main() {
        let options = [SessionRequest.Option(id: "yes", label: "Yes", description: ""), SessionRequest.Option(id: "no", label: "No", description: "")]
        let scope = SessionRequest.Question(question: "Which scope?", header: "Scope", multiSelect: false, options: options)
        let targets = SessionRequest.Question(question: "Which targets?", header: "Targets", multiSelect: true,
                                              options: [SessionRequest.Option(id: "mac", label: "Mac", description: ""), SessionRequest.Option(id: "iphone", label: "iPhone", description: ""), SessionRequest.Option(id: "web", label: "Web", description: "")])

        var oneTap = QuestionPager(questions: [scope])
        check(oneTap.isOneTap && oneTap.isSingle && !oneTap.onReview, "one single-select question is one tap")
        check(oneTap.pick(1) == RequestDecision(choices: ["no"]), "a pick answers a one-tap question at once, by the option's id")
        oneTap.type("  Maybe  ")
        check(oneTap.submit() == RequestDecision(choices: [], answers: ["Which scope?": "Maybe"]), "a typed answer goes under the question's text, trimmed")
        oneTap.go(to: 1)
        check(oneTap.step == 0, "a single question has no other step")

        var single = QuestionPager(questions: [targets])
        check(!single.isOneTap && single.isSingle, "one multi-select question has no chips")
        check(single.submit() == nil, "an unanswered single question does not send")
        check(!single.choose(2) && !single.choose(0) && !single.choose(7), "a toggle never sends")
        check(single.submit() == RequestDecision(choices: ["mac", "web"]), "a multi-select question sends one id per pick, in option order: \(String(describing: single.decision))")
        check(single.answer(at: 0) == "Mac, Web", "the review shows the picks by label")
        _ = single.choose(0)
        check(single.decision == RequestDecision(choices: ["web"]), "a second toggle clears the pick")

        let same = SessionRequest.Question(question: "Which copies?", header: "", multiSelect: true,
                                           options: [SessionRequest.Option(id: "first", label: "same", description: "first"), SessionRequest.Option(id: "second", label: "same", description: "second")])
        var twins = QuestionPager(questions: [same])
        _ = twins.choose(0)
        _ = twins.choose(1)
        check(twins.submit() == RequestDecision(choices: ["first", "second"]), "options with the same label are told apart by id")
        _ = twins.choose(0)
        check(twins.decision == RequestDecision(choices: ["second"]) && twins.isPicked(1, at: 0) && !twins.isPicked(0, at: 0), "unpicking one leaves its twin")

        var pager = QuestionPager(questions: [scope, targets])
        check(!pager.isSingle && pager.reviewStep == 2 && pager.step == 0, "two questions start on the first page")
        check(!pager.choose(0) && pager.step == 1, "a single-select pick advances")
        check(pager.isAnswered(0) && !pager.isAnswered(1) && !pager.canSubmit, "only the first is answered")
        _ = pager.choose(1)
        check(pager.step == 1, "a multi-select toggle stays on its page")
        check(!pager.next() && pager.onReview, "next moves to the review page")
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
        check(pager.submit() == RequestDecision(choices: ["no", "iphone"]), "review sends every question's ids")

        var gated = QuestionPager(questions: [scope, targets])
        gated.go(to: 1)
        gated.type("All of them")
        gated.go(to: 2)
        check(!gated.canSubmit && gated.submit() == nil && gated.onReview, "review does not send while a question is unanswered")
        gated.go(to: 0)
        _ = gated.choose(0)
        check(gated.canSubmit && gated.decision == RequestDecision(choices: ["yes"], answers: ["Which targets?": "All of them"]), "a typed question goes in answers, a picked one in choices")

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

        var ask = drafts.pager(for: SessionRequest(id: "ask-000000000001", kind: "ask", tool: "", questions: [scope]))
        check(ask.ask && !ask.isOneTap && ask.isSingle, "an ask is never answered on one tap")
        check(ask.pick(1) == nil && ask.step == 0 && ask.answer(at: 0) == "No", "picking an ask's option waits for submit")
        check(ask.decision == RequestDecision(choices: ["no"]), "an empty note is left out")
        ask.note = "  After lunch.  "
        check(ask.submit() == RequestDecision(choices: ["no"], message: "After lunch."), "the note is sent trimmed as the message")
        var question = QuestionPager(questions: [scope, targets])
        question.note = "ignored"
        _ = question.choose(0)
        _ = question.choose(2)
        check(question.decision == RequestDecision(choices: ["yes", "web"]), "a question never carries a note")
        var typedAsk = QuestionPager(questions: [scope], ask: true)
        _ = typedAsk.choose(0)
        typedAsk.chooseOther()
        check(typedAsk.isOtherPicked(at: 0) && !typedAsk.isPicked(0, at: 0) && typedAsk.decision == nil, "picking Other clears the option and waits for text")
        typedAsk.type("Tomorrow")
        check(typedAsk.submit() == RequestDecision(choices: [], answers: ["Which scope?": "Tomorrow"]), "Other sends the typed answer")
        _ = typedAsk.choose(1)
        check(!typedAsk.isOtherPicked(at: 0) && typedAsk.decision == RequestDecision(choices: ["no"]), "an option after Other replaces it")
        let deliveryJSON = #"{"permissionMode":"","requests":[{"id":"ask-0fca204fdeb3","kind":"ask","tool":"","title":"Message not delivered","optionsOnly":true,"questions":[{"question":"It failed: \"Run the tests\"","header":"","multiSelect":false,"options":[{"id":"retry","label":"Retry","description":"send it again"},{"id":"dismiss","label":"Dismiss","description":"leave it as it is"}],"suggested":0}],"alwaysAllow":false}]}"#
        let deliveryRequest = try! JSONDecoder().decode(SessionRequests.self, from: Data(deliveryJSON.utf8)).requests[0]
        check(deliveryRequest.optionsOnly && !SessionRequest(id: "q", kind: "question", tool: "AskUserQuestion").optionsOnly, "optionsOnly decodes, and is off unless the request says so")
        var delivery = QuestionPagerDrafts(limit: 2).pager(for: deliveryRequest)
        check(delivery.optionsOnly && !delivery.takesText && !delivery.takesNote, "an options-only ask takes no typed answer and no note")
        delivery.note = "please"
        delivery.chooseOther()
        delivery.type("something else")
        check(!delivery.isOtherPicked(at: 0) && delivery.typedAnswer(at: 0).isEmpty, "Other and typed text are refused")
        _ = delivery.choose(1)
        check(delivery.submit() == RequestDecision(choices: ["dismiss"]), "it sends the picked option alone, without the note")
        let modeDrafts = QuestionPagerDrafts(limit: 2)
        var plainAsk = SessionRequest(id: "ask-0fca204fdeb3", kind: "ask", tool: "", questions: deliveryRequest.questions)
        var typedDraft = modeDrafts.pager(for: plainAsk)
        typedDraft.type("in my own words")
        modeDrafts.save(typedDraft, for: plainAsk.id)
        check(modeDrafts.pager(for: plainAsk).typedAnswer(at: 0) == "in my own words", "a draft comes back for the same request")
        plainAsk.optionsOnly = true
        let reread = modeDrafts.pager(for: plainAsk)
        check(reread.optionsOnly && reread.typedAnswer(at: 0).isEmpty, "a request reread as options-only starts a fresh pager, not the typed draft")
        var askPair = QuestionPager(questions: [scope, targets], ask: true)
        check(!askPair.choose(0) && askPair.step == 1, "a pick in a several-question ask still pages on")
        print("question pager ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition { fatalError(message) }
    }
}
