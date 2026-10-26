import Foundation

extension TimelineItem {
    var eventTitle: String {
        [title, status ?? ""].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    var eventLine: String {
        let text = self.text.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? eventTitle : "\(eventTitle): \(text)"
    }
}
