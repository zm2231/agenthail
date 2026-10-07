import Combine
import SwiftUI

@MainActor
final class VoiceCallCoordinator: ObservableObject {
    @Published private(set) var model: VoiceOperatorModel?
    private let makeModel: () -> VoiceOperatorModel
    private var observation: AnyCancellable?

    init(makeModel: @escaping () -> VoiceOperatorModel = { VoiceOperatorModel() }) {
        self.makeModel = makeModel
    }

    func open(targetID: String = "") {
        if model == nil {
            let call = makeModel()
            model = call
            observation = call.objectWillChange.sink { [weak self] _ in self?.objectWillChange.send() }
        }
        guard let model else { return }
        if !model.hasLocalCall && model.state?.hasCall != true && !model.hostHangupPending && !model.hostHangupUnconfirmed {
            model.targetID = targetID
        }
        model.open()
    }

    func disconnect() {
        model?.close()
        observation = nil
        model = nil
    }
}
