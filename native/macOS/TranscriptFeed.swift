import Observation

@MainActor
@Observable
final class TranscriptFeed {
    private(set) var items: [TimelineItem] = []

    func show(_ next: [TimelineItem]) {
        if next != items { items = next }
    }
}
