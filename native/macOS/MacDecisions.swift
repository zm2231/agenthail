import Foundation
import SwiftUI

extension DecisionInbox {
    static func acceptsRecommended(_ modifiers: EventModifiers) -> Bool {
        modifiers.subtracting(.capsLock) == .shift
    }
}

extension AgenthailModel {
    static let decisionTimeout: TimeInterval = 6

    var decisionsKey: String {
        let hosts = (daemon?.hosts ?? []).map { "\($0.id):\($0.state)" }.joined(separator: ",")
        return "\(decisionsRev ?? "older"):\(hosts)"
    }

    func refreshDecisionsSoon() async {
        try? await Task.sleep(for: .milliseconds(300))
        guard !Task.isCancelled else { return }
        decisionFeed.refresh()
    }

    func loadDecisions() async -> DecisionInbox {
        guard let api else { return DecisionInbox(unreachable: [snapshot?.daemon.host?.name ?? "This Mac"]) }
        let local = snapshot?.daemon.host?.name ?? "This Mac"
        let paired = snapshot?.daemon.hosts ?? []
        var inbox = DecisionInbox()
        if snapshot != nil && decisionsRev == nil { inbox.outdated.append(local) }
        for host in paired where host.state != "connected" {
            if host.state == "incompatible" { inbox.outdated.append(host.name) } else { inbox.unreachable.append(host.name) }
        }
        let asked: [(id: String, name: String)] = [("", local)] + paired.filter { $0.state == "connected" }.map { ($0.id, $0.name) }
        await withTaskGroup(of: (String, String, Result<DecisionsPage, Error>).self) { group in
            for host in asked {
                group.addTask {
                    do { return (host.id, host.name, .success(try await api.decisions(host: host.id.isEmpty ? nil : host.id, timeout: Self.decisionTimeout))) }
                    catch { return (host.id, host.name, .failure(error)) }
                }
            }
            for await (id, name, result) in group {
                switch result {
                case .success(let page): inbox.add(page, host: id) { id.isEmpty || $0.contains("::") ? $0 : id + "::" + $0 }
                case .failure(AgenthailAPIError.olderHost): inbox.outdated.append(name)
                case .failure: inbox.unreachable.append(name)
                }
            }
        }
        inbox.unreachable.sort()
        inbox.outdated.sort()
        return inbox
    }

    func answerDecision(_ request: SessionRequest, sessionID: String, decision: RequestDecision) async -> DecisionAnswer {
        guard let api else { return .failed(RequestAnswerFailure(AgenthailAPIError.unavailable("Agenthail isn't connected."))) }
        do {
            _ = try await api.respond(sessionID: sessionID, requestID: request.id, decision: decision)
            return .answered
        } catch {
            return .failed(RequestAnswerFailure(error))
        }
    }
}
