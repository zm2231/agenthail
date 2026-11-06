import SwiftUI

extension EnvironmentValues {
    @Entry var cachedAttachment: @MainActor (TimelineItem) -> Data? = { _ in nil }
}
