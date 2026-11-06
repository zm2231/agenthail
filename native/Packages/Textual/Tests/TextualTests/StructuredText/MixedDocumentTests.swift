#if os(iOS) && !targetEnvironment(macCatalyst)
  import SwiftUI
  import Testing
  import SnapshotTesting

  import Textual

  extension StructuredText {
    @MainActor
    struct MixedDocumentTests {
      nonisolated static let markdown = """
        # Reconnect is fixed

        The pending build survives reconnecting. **All 8 regression tests pass**, and `restore()` keeps the identity.

        ## What changed

        - Preserved the session identity
          - Kept the queued instruction attached
            1. Saved before the drop
            2. Replayed after
        - Added coverage for interrupted delivery

        > The queue keeps its order across reconnects.
        >
        > - Even when two hosts answer

        ```swift
        let session = restore(keeping: identity)
        await session.resume()
        ```

        | Check | Result |
        | --- | --- |
        | Reconnect | Passed |
        | Queue identity | Passed |

        ---

        The latest update is visible above the composer.
        """

      private let layout = SwiftUISnapshotLayout.device(config: .iPhone8)

      @Test func defaultStyle() {
        let view = StructuredText(markdown: Self.markdown)
          .background(Color.guide)
          .padding(.horizontal)

        assertSnapshot(of: view, as: .image(precision: 1, layout: layout))
      }

      @Test func gitHubStyle() {
        let view = StructuredText(markdown: Self.markdown)
          .textual.structuredTextStyle(.gitHub)
          .background(Color.guide)
          .padding(.horizontal)

        assertSnapshot(of: view, as: .image(precision: 1, layout: layout))
      }
    }
  }
#endif
