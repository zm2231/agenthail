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
        let rows = perID.sorted { $0.key < $1.key }.flatMap { name, ids in
            ids.map { id, runs in "\(name)#\(id.map { $0 == " " || $0 == "=" ? "_" : $0 }.map(String.init).joined())=\(runs)" }
        }
        return (["busyMs=\(Int(busy * 1000))"] + counts.sorted { $0.key < $1.key }.map { "\($0.key)=\($0.value)" } + rows).joined(separator: " ")
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

    func countsScreenChurn() -> some View {
        onGeometryChange(for: CGSize.self) { $0.size } action: { _ in BodyRuns.hit("screen.size") }
            .onGeometryChange(for: EdgeInsets.self) { $0.safeAreaInsets } action: { _ in BodyRuns.hit("screen.safeArea") }
            .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillChangeFrameNotification)) { _ in BodyRuns.hit("kb.frame") }
            .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillShowNotification)) { _ in BodyRuns.hit("kb.show") }
            .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillHideNotification)) { _ in BodyRuns.hit("kb.hide") }
            .background(ScreenTraitsProbe())
    }
}

private struct ScreenTraitsProbe: View {
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.dynamicTypeSize) private var typeSize
    @Environment(\.horizontalSizeClass) private var horizontal
    @Environment(\.verticalSizeClass) private var vertical
    @Environment(\.displayScale) private var displayScale
    @Environment(\.layoutDirection) private var direction
    @Environment(\.legibilityWeight) private var legibility
    @Environment(\.isFocused) private var focused

    var body: some View {
        let _ = BodyRuns.hit("screen.traits")
        let _ = BodyRuns.changed("screen.trait.scenePhase", "root", scenePhase)
        let _ = BodyRuns.changed("screen.trait.colorScheme", "root", colorScheme)
        let _ = BodyRuns.changed("screen.trait.typeSize", "root", typeSize)
        let _ = BodyRuns.changed("screen.trait.sizeClass", "root", [horizontal, vertical])
        let _ = BodyRuns.changed("screen.trait.displayScale", "root", displayScale)
        let _ = BodyRuns.changed("screen.trait.direction", "root", direction)
        let _ = BodyRuns.changed("screen.trait.legibility", "root", legibility)
        let _ = BodyRuns.changed("screen.trait.focused", "root", focused)
        Color.clear
    }
}
#else
import SwiftUI

extension View {
    func countsAppearances(_ name: String) -> some View { self }
    func countsDisappearances(_ name: String) -> some View { self }
    func countsGeometryChanges(_ name: String) -> some View { self }
    func countsScreenChurn() -> some View { self }
}
#endif
