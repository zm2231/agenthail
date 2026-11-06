// Derived in body so a lazy stack's first measurement is the final size.
@MainActor
final class Memo<Key: Equatable, Value> {
  private var entry: (key: Key, value: Value)?

  func value(for key: Key, _ make: (Key) -> Value) -> Value {
    if let entry, entry.key == key { return entry.value }
    let value = make(key)
    entry = (key, value)
    return value
  }
}
