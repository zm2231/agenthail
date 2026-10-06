import Foundation

struct TurnSettings: Codable, Equatable, Sendable {
    enum Mode: String, Codable, CaseIterable, Sendable {
        case `default`
        case plan
    }

    enum ServiceTier: String, Codable, CaseIterable, Sendable {
        case `default`
        case fast
        case flex
    }

    enum SchemaError: LocalizedError, Equatable {
        case notObject
        case tooLarge

        var errorDescription: String? {
            switch self {
            case .notObject: return "The output schema must be a JSON object."
            case .tooLarge: return "The output schema must be at most 64 KiB."
            }
        }
    }

    var effort: String?
    var mode: Mode?
    var serviceTier: ServiceTier?
    var outputSchema: RecordedJSON?

    init(effort: String? = nil, mode: Mode? = nil, serviceTier: ServiceTier? = nil, outputSchema: RecordedJSON? = nil) {
        self.effort = effort
        self.mode = mode
        self.serviceTier = serviceTier
        self.outputSchema = outputSchema
    }

    var isEmpty: Bool { effort == nil && mode == nil && serviceTier == nil && outputSchema == nil }

    static func outputSchema(from text: String) throws -> RecordedJSON? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let data = Data(trimmed.utf8)
        guard data.count <= 64 << 10 else { throw SchemaError.tooLarge }
        guard let schema = try? JSONDecoder().decode(RecordedJSON.self, from: data), case .object = schema else { throw SchemaError.notObject }
        return schema
    }

    enum CodingKeys: String, CodingKey {
        case effort
        case mode
        case serviceTier
        case outputSchema
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encodeIfPresent(effort, forKey: .effort)
        try container.encodeIfPresent(mode, forKey: .mode)
        try container.encodeIfPresent(serviceTier, forKey: .serviceTier)
        try container.encodeIfPresent(outputSchema, forKey: .outputSchema)
    }
}
