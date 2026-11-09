import SwiftUI

extension EnvironmentValues {
    @Entry var cachedAttachment = RowAction<TimelineItem, Data?>(owner: "") { _ in nil }
}
