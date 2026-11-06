import Foundation

@MainActor
final class TimelineMemo {
    private var latest: [TimelineItem] = []
    private var older: [TimelineItem] = []
    private var fold = true
    private var merged: [TimelineItem] = []
    private var grouped: [TimelineGroup]?
    private var primed = false

    func items(latest: [TimelineItem], older: [TimelineItem]) -> [TimelineItem] {
        if primed, latest == self.latest, older == self.older { return merged }
        primed = true
        self.latest = latest
        self.older = older
        let ids = Set(latest.map(\.id))
        merged = older.filter { !ids.contains($0.id) } + latest
        grouped = nil
        return merged
    }

    func groups(latest: [TimelineItem], older: [TimelineItem], foldAgents: Bool) -> [TimelineGroup] {
        let items = items(latest: latest, older: older)
        if let grouped, fold == foldAgents { return grouped }
        fold = foldAgents
        let made = TimelineGroup.make(items, foldAgents: foldAgents)
        grouped = made
        return made
    }
}
