import Foundation

/// Type-erased Codable value for handling arbitrary JSON payloads (json.RawMessage equivalent).
public enum AnyCodable: Sendable {
    case string(String)
    case int(Int)
    case double(Double)
    case bool(Bool)
    case array([AnyCodable])
    case dictionary([String: AnyCodable])
    case null
}

extension AnyCodable: Decodable {
    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()

        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Int.self) {
            self = .int(value)
        } else if let value = try? container.decode(Double.self) {
            self = .double(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([AnyCodable].self) {
            self = .array(value)
        } else if let value = try? container.decode([String: AnyCodable].self) {
            self = .dictionary(value)
        } else {
            throw DecodingError.dataCorruptedError(
                in: container,
                debugDescription: "Unable to decode AnyCodable value"
            )
        }
    }
}

extension AnyCodable: Encodable {
    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case let .string(value): try container.encode(value)
        case let .int(value): try container.encode(value)
        case let .double(value): try container.encode(value)
        case let .bool(value): try container.encode(value)
        case let .array(value): try container.encode(value)
        case let .dictionary(value): try container.encode(value)
        case .null: try container.encodeNil()
        }
    }
}

extension AnyCodable {
    /// Extract the underlying string value, if present.
    public var stringValue: String? {
        if case let .string(v) = self { return v }
        return nil
    }

    /// Extract the underlying int value, if present.
    public var intValue: Int? {
        if case let .int(v) = self { return v }
        return nil
    }

    /// Extract the underlying bool value, if present.
    public var boolValue: Bool? {
        if case let .bool(v) = self { return v }
        return nil
    }

    /// Extract the underlying numeric value as Double, if the case is
    /// numeric. Used by `HiveKPISnapshot` to coerce the operator's
    /// `map[string]any` payload into typed metric numbers.
    public var doubleValue: Double? {
        switch self {
        case let .double(v): return v
        case let .int(v): return Double(v)
        default: return nil
        }
    }
}
