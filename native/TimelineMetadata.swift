import Foundation

enum TimelineMetadata {
    /// Titles, call ids, statuses and raw timestamps are for debugging; a reader only needs to know the host shortened a record.
    static let shortened = "Shortened by the host"

    static func note(_ item: TimelineItem) -> String? {
        item.truncated ? shortened : nil
    }
}
