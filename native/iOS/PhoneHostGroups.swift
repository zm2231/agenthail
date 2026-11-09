import Foundation

struct HostGroups: Equatable {
    var channels: [ChannelState] = []
    var relays: [RelayState] = []
}

struct HostGroupSession: Identifiable, Equatable {
    let id: String
    let title: String
}

extension AgenthailIOSModel {
    func groupSessions(on hostID: String, writableOnly: Bool) -> [HostGroupSession] {
        let prefix = hostID + HostCatalog.separator
        return (snapshot?.sessions ?? []).compactMap { session in
            guard session.id.hasPrefix(prefix), session.archived != true, !writableOnly || !session.isReadOnly else { return nil }
            return HostGroupSession(id: String(session.id.dropFirst(prefix.count)), title: cells.title(of: session))
        }
    }

    func groupName(_ id: String, on hostID: String) -> String {
        session(HostCatalog.qualify(hostID, id)).map(cells.title(of:)) ?? id
    }

    func groupAction(on hostID: String, _ action: String, channel: String? = nil, targetID: String? = nil, fromID: String? = nil, toID: String? = nil, pattern: String? = nil, once: Bool? = nil, handoff: String? = nil, relayID: Int64? = nil) async throws {
        let host = try groupHost(hostID)
        try await host.api.action(action, channel: channel, targetID: targetID, fromID: fromID, toID: toID, pattern: pattern, once: once, handoff: handoff, relayID: relayID)
        _ = await host.refresh(fresh: true)
    }

    func sendToChannel(_ channel: String, on hostID: String, message: String, idempotencyKey: String) async throws -> String {
        let host = try groupHost(hostID)
        let result = try await host.api.channelSend(channel, message: message, idempotencyKey: idempotencyKey)
        let sent = ProjectBroadcast.sent(result) { groupName($0, on: hostID) }
        _ = await host.refresh(fresh: true)
        return sent
    }

    private func groupHost(_ hostID: String) throws -> HostConnection {
        guard let host = connection(hostID), host.shownReachable else {
            throw AgenthailAPIError.unavailable("Connect to \(hosts.first { $0.id == hostID }?.name ?? "this computer") first.")
        }
        return host
    }
}
