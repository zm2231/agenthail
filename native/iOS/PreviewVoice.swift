#if DEBUG
import Foundation
import SwiftUI

@MainActor
enum PreviewVoice {
    static func model() -> VoiceOperatorModel {
        ProcessInfo.processInfo.arguments.contains("--preview-voice-unreachable")
            ? VoiceOperatorModel(api: UnreachableVoiceService())
            : VoiceOperatorModel(preview: true)
    }
}

@MainActor
private final class UnreachableVoiceService: VoiceServiceClient {
    func request(path: String) -> URLRequest { URLRequest(url: URL(string: "https://preview.invalid")!.appendingPathComponent(path)) }
    func state() async throws -> VoiceState { throw URLError(.cannotConnectToHost) }
    func action(_ action: VoiceAction) async throws -> VoiceState { throw URLError(.cannotConnectToHost) }
}

@MainActor
enum SceneEvaluations {
    private static var count = 0

    static func next() -> Int {
        count += 1
        return count
    }
}

struct SceneEvaluationStamp: View {
    let count: Int

    var body: some View {
        Text("\(count)").font(.system(size: 1)).opacity(0.02).frame(width: 1, height: 1)
            .accessibilityIdentifier("scene-evaluations")
            .accessibilityLabel("\(count)")
    }
}
#endif
