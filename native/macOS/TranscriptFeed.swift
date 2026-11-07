import Observation

@MainActor
@Observable
final class TranscriptFeed {
    private(set) var revision = 0

    func streamed() { revision &+= 1 }
}
