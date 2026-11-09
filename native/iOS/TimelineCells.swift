import Observation
import SwiftUI

@Observable
final class TimelineCell {
    var item: TimelineItem

    init(_ item: TimelineItem) { self.item = item }
}

// One observed cell per row of the open session, so text streamed into one message redraws only that row.
@MainActor
final class TimelineCells {
    private var cells: [String: TimelineCell] = [:]

    func cell(_ id: String) -> TimelineCell? { cells[id] }

    func adopt(_ items: [TimelineItem]) {
        var next: [String: TimelineCell] = [:]
        for item in items where next[item.id] == nil {
            if let cell = cells[item.id] {
                if cell.item != item { cell.item = item }
                next[item.id] = cell
            } else {
                next[item.id] = TimelineCell(item)
            }
        }
        cells = next
    }
}

extension EnvironmentValues {
    @Entry var timelineCells: TimelineCells? = nil
}

struct LiveTimelineRow: View {
    let item: TimelineItem
    var compactContext = true
    var finished: String? = nil
    var bodyLoader: ((TimelineItem) async -> RetainedBodyResult?)? = nil
    var attachmentLoader: ((TimelineItem) async -> Data?)? = nil
    @Environment(\.timelineCells) private var cells

    var body: some View {
        IOSTimelineRow(item: cells?.cell(item.id)?.item ?? item, compactContext: compactContext, finished: finished, bodyLoader: bodyLoader, attachmentLoader: attachmentLoader)
    }
}
