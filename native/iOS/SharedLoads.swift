import Foundation

// A caller that is cancelled leaves the load running, so the next caller joins it instead of fetching again.
@MainActor
final class SharedLoads<Value: Sendable> {
    private var running: [String: Task<Value, Never>] = [:]

    func value(for key: String, _ load: @escaping @MainActor () async -> Value) async -> Value {
        if let task = running[key] { return await task.value }
        let task = Task { @MainActor in await load() }
        running[key] = task
        let value = await task.value
        if running[key] == task { running[key] = nil }
        return value
    }
}
