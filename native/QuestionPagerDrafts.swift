import Foundation

@MainActor
final class QuestionPagerDrafts {
    static let shared = QuestionPagerDrafts()

    private let limit: Int
    private var pagers: [String: QuestionPager] = [:]
    private var order: [String] = []

    init(limit: Int = 16) {
        self.limit = limit
    }

    func pager(for request: SessionRequest) -> QuestionPager {
        let questions = request.questions ?? []
        if let saved = pagers[request.id], saved.questions == questions { return saved }
        return QuestionPager(questions: questions)
    }

    func save(_ pager: QuestionPager, for requestID: String) {
        order.removeAll { $0 == requestID }
        order.append(requestID)
        pagers[requestID] = pager
        while order.count > limit {
            pagers[order.removeFirst()] = nil
        }
    }
}
