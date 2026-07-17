import CryptoKit
import DeviceCheck
import Foundation

struct PushRegistration: Codable {
    let installationId: String
    let credential: String
    let expiresAt: Int64?

    var needsRenewal: Bool {
        guard let expiresAt else { return true }
        let renewalThreshold = Date().addingTimeInterval(7 * 24 * 60 * 60).timeIntervalSince1970 * 1000
        return expiresAt <= Int64(renewalThreshold)
    }
}

final class PushRelayClient: Sendable {
    private let baseURL: URL
    private let session: URLSession

    init(baseURL: URL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
    }

    static var environment: String {
        Bundle.main.object(forInfoDictionaryKey: "AgenthailAPNSEnvironment") as? String ?? "sandbox"
    }

    func register(deviceToken: String) async throws -> PushRegistration {
        let challenge: AppAttestChallengeResponse = try await request("/v1/attest/challenge", method: "POST", body: EmptyRequest())
        guard let challengeData = Data(base64URLEncoded: challenge.challenge) else {
            throw PushRelayError.invalidChallenge
        }
        let service = DCAppAttestService.shared
        guard service.isSupported else { throw PushRelayError.appAttestUnavailable }
        let keyID = try await service.generateKey()
        let clientDataHash = Data(SHA256.hash(data: challengeData))
        let attestation = try await service.attestKey(keyID, clientDataHash: clientDataHash)
        let body = PushRegistrationRequest(
            deviceToken: deviceToken,
            environment: Self.environment,
            challengeId: challenge.challengeId,
            keyId: keyID,
            attestation: attestation.base64EncodedString()
        )
        return try await request("/v1/register", method: "POST", body: body)
    }

    func revoke(_ registration: PushRegistration) async throws {
        let _: PushRelayOK = try await request("/v1/register", method: "DELETE", body: ["installationId": registration.installationId, "credential": registration.credential])
    }

    func validate(_ registration: PushRegistration) async throws {
        let _: PushRelayOK = try await request("/v1/register/check", method: "POST", body: ["installationId": registration.installationId, "credential": registration.credential])
    }

    private func request<T: Decodable, Body: Encodable>(_ path: String, method: String, body: Body) async throws -> T {
        var request = URLRequest(url: URL(string: path, relativeTo: baseURL)!)
        request.httpMethod = method
        request.httpBody = try JSONEncoder().encode(body)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.timeoutInterval = 20
        let (data, response) = try await session.data(for: request)
        guard let response = response as? HTTPURLResponse else { throw AgenthailAPIError.invalidResponse }
        guard (200..<300).contains(response.statusCode) else {
            let detail = (try? JSONSerialization.jsonObject(with: data) as? [String: Any])?["error"] as? String ?? "Push registration failed."
            throw AgenthailAPIError.request(response.statusCode, detail)
        }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

private struct EmptyRequest: Encodable {}

private struct AppAttestChallengeResponse: Decodable {
    let challengeId: String
    let challenge: String
}

private struct PushRegistrationRequest: Encodable {
    let deviceToken: String
    let environment: String
    let challengeId: String
    let keyId: String
    let attestation: String
}

private enum PushRelayError: LocalizedError {
    case appAttestUnavailable
    case invalidChallenge

    var errorDescription: String? {
        switch self {
        case .appAttestUnavailable:
            return "This device cannot verify the Agenthail app."
        case .invalidChallenge:
            return "The notification service returned an invalid verification challenge."
        }
    }
}

private extension Data {
    init?(base64URLEncoded value: String) {
        var normalized = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        normalized += String(repeating: "=", count: (4 - normalized.count % 4) % 4)
        self.init(base64Encoded: normalized)
    }
}

private struct PushRelayOK: Decodable {
    let ok: Bool
}
