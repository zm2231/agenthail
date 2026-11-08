import Foundation

extension AgenthailModel {
    func createPairing(interval: Duration = PairingProgress.interval) {
        guard let api else { return }
        cancelPairing()
        pairingNotice = nil
        pairingWatch = Task {
            let created: PairingResponse
            do {
                created = try await api.createPairing(name: "iPhone")
            } catch {
                if !Task.isCancelled { operationError = AgenthailAPIError.userMessage(error) }
                return
            }
            guard !Task.isCancelled else { return }
            pairing = created
            operationError = nil
            await watchPairing(created, api: api, interval: interval)
        }
    }

    func cancelPairing() {
        pairingWatch?.cancel()
        pairingWatch = nil
        pairing = nil
    }

    func watchPairing(_ created: PairingResponse, api: AgenthailAPI, interval: Duration = PairingProgress.interval) async {
        while !Task.isCancelled {
            try? await Task.sleep(for: interval)
            guard !Task.isCancelled, pairing?.id == created.id else { return }
            let status = try? await api.pairingStatus(id: created.id)
            guard !Task.isCancelled, pairing?.id == created.id else { return }
            let progress = PairingProgress.of(status, pairing: created, now: Date())
            guard progress != .waiting else { continue }
            pairing = nil
            pairingNotice = progress.notice
            if case .paired = progress { await loadDevices() }
            return
        }
    }
}
