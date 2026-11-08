import Foundation

enum OutputSchemaInput {
    static let limit = 64 << 10

    static func parse(_ text: String) -> Result<Any?, OutputSchemaProblem> {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return .success(nil) }
        guard let schema = try? JSONSerialization.jsonObject(with: Data(trimmed.utf8)), schema is [String: Any] else {
            return .failure(OutputSchemaProblem(message: "The output schema must be a JSON object."))
        }
        guard let sent = try? JSONSerialization.data(withJSONObject: schema), sent.count <= limit else {
            return .failure(OutputSchemaProblem(message: "The output schema must be at most 64 KiB."))
        }
        return .success(schema)
    }

    static func recorded(_ text: String) -> Result<RecordedJSON?, OutputSchemaProblem> {
        parse(text).flatMap { schema in
            guard let schema else { return .success(nil) }
            guard let data = try? JSONSerialization.data(withJSONObject: schema), let value = try? JSONDecoder().decode(RecordedJSON.self, from: data) else {
                return .failure(OutputSchemaProblem(message: "The output schema must be a JSON object."))
            }
            return .success(value)
        }
    }

    static func problem(_ text: String) -> String? {
        if case .failure(let problem) = parse(text) { return problem.message }
        return nil
    }
}

struct OutputSchemaProblem: LocalizedError {
    let message: String
    var errorDescription: String? { message }
}
