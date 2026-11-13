#if DEBUG
import SwiftUI

@MainActor
enum BodyRuns {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--body-runs")
    private static var counts: [String: Int] = [:]
    private static var observer: CFRunLoopObserver?
    private static var awoke: CFAbsoluteTime = 0
    private static var busy: CFAbsoluteTime = 0

    static func hit(_ name: String) {
        guard enabled else { return }
        counts[name, default: 0] += 1
    }

    static var summary: String {
        (["busyMs=\(Int(busy * 1000))"] + counts.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" }).joined(separator: " ")
    }

    static func watchMainThread() {
        guard enabled, observer == nil else { return }
        let activities = CFRunLoopActivity.afterWaiting.rawValue | CFRunLoopActivity.beforeWaiting.rawValue
        observer = CFRunLoopObserverCreateWithHandler(nil, activities, true, 0) { _, activity in
            MainActor.assumeIsolated {
                let now = CFAbsoluteTimeGetCurrent()
                if activity == .afterWaiting {
                    awoke = now
                } else if awoke > 0 {
                    busy += now - awoke
                    awoke = 0
                }
            }
        }
        CFRunLoopAddObserver(CFRunLoopGetMain(), observer, .commonModes)
    }
}

struct BodyRunsProbe: View {
    var body: some View {
        TimelineView(.periodic(from: .now, by: 0.25)) { _ in
            let summary = BodyRuns.summary
            Text(summary).font(.system(size: 1)).opacity(0.02).frame(width: 1, height: 1)
                .accessibilityIdentifier("body-runs")
                .accessibilityLabel(summary)
        }
        .onAppear(perform: BodyRuns.watchMainThread)
    }
}

extension View {
    func countsAppearances(_ name: String) -> some View {
        onAppear { BodyRuns.hit(name) }
    }
}
#else
import SwiftUI

extension View {
    func countsAppearances(_ name: String) -> some View { self }
}
#endif
