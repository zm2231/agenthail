#if DEBUG
import Combine
import Observation
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

    private static var perID: [String: [String: Int]] = [:]

    static func hit(_ name: String, id: String) {
        guard enabled else { return }
        perID[name, default: [:]][id, default: 0] += 1
    }

    private static var seen: [String: Any] = [:]
    private static var armed: Set<String> = []
    private static var watched: [String: AnyCancellable] = [:]

    static func changed<Value: Equatable>(_ name: String, _ id: String, _ value: Value) {
        guard enabled else { return }
        let key = name + "|" + id
        if let old = seen[key] as? Value, old != value { hit(name) }
        seen[key] = value
    }

    static func compared(_ name: String, _ fields: KeyValuePairs<String, Bool>) {
        guard enabled else { return }
        hit("eq." + name)
        for (field, same) in fields where !same { hit("eq.\(name).\(field)") }
    }

    static func observe(_ name: String, _ id: String, _ read: () -> Void) {
        guard enabled else { return }
        let key = name + "|" + id
        guard armed.insert(key).inserted else { return }
        withObservationTracking(read) {
            MainActor.assumeIsolated {
                armed.remove(key)
                hit(name)
            }
        }
    }

    static func watch(_ name: String, _ object: some ObservableObject) {
        guard enabled else { return }
        let key = name + "|\(ObjectIdentifier(object))"
        guard watched[key] == nil else { return }
        watched[key] = object.objectWillChange.sink { _ in MainActor.assumeIsolated { hit(name) } }
    }

    static var summary: String {
        let spread = perID.sorted { $0.key < $1.key }.map { name, ids in
            let runs = ids.values.sorted(by: >)
            return "\(name).ids=\(runs.count) \(name).max=\(runs.first ?? 0) \(name).over3=\(runs.filter { $0 > 3 }.count)"
        }
        return (["busyMs=\(Int(busy * 1000))"] + counts.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" } + spread).joined(separator: " ")
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

    func countsDisappearances(_ name: String) -> some View {
        onDisappear { BodyRuns.hit(name) }
    }

    func countsGeometryChanges(_ name: String) -> some View {
        onGeometryChange(for: CGFloat.self) { $0.frame(in: .named(TranscriptRows.contentSpace)).minY } action: { _ in BodyRuns.hit(name) }
    }
}
#else
import SwiftUI

extension View {
    func countsAppearances(_ name: String) -> some View { self }
    func countsDisappearances(_ name: String) -> some View { self }
    func countsGeometryChanges(_ name: String) -> some View { self }
}
#endif
