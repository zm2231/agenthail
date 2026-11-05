import Foundation

@MainActor
enum PreviewFollowStuck {
#if DEBUG
    static var enabled = ProcessInfo.processInfo.arguments.contains("--preview-follow-stuck")
#else
    static let enabled = false
#endif
}
