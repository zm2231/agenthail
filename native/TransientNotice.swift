import Foundation
import Observation

@MainActor
@Observable
final class TransientNotice {
    static let duration: Duration = .seconds(4)

    private(set) var text: String?
    private(set) var shown = 0

    func show(_ text: String, for duration: Duration = TransientNotice.duration) {
        let text = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        shown += 1
        let mine = shown
        self.text = text
        Task { [weak self] in
            try? await Task.sleep(for: duration)
            guard let self, self.shown == mine else { return }
            self.text = nil
        }
    }

    func dismiss() {
        shown += 1
        text = nil
    }
}

extension ProjectChange {
    var error: String? {
        switch self {
        case .done: nil
        case .confirmReplace(let message), .failed(let message): message
        }
    }

    @MainActor
    func announce(in notice: TransientNotice) {
        if case .done(let message) = self { notice.show(message) } else { notice.dismiss() }
    }
}
