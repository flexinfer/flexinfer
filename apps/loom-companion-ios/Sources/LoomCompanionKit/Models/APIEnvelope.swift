import Foundation

/// Standard response envelope matching `mobileEnvelope` from api_mobile.go.
public struct APIEnvelope<T: Decodable>: Decodable {
    public let ok: Bool
    public let data: T?
    public let error: APIErrorBody?
    public let meta: APIMeta

    enum CodingKeys: String, CodingKey {
        case ok, data, error, meta
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.ok = (try? container.decodeIfPresent(Bool.self, forKey: .ok)) ?? false
        self.data = try? container.decodeIfPresent(T.self, forKey: .data)
        self.error = try? container.decodeIfPresent(APIErrorBody.self, forKey: .error)
        self.meta = (try? container.decodeIfPresent(APIMeta.self, forKey: .meta)) ?? APIMeta()
    }
}

public struct APIMeta: Decodable, Sendable {
    public let requestId: String
    public let timestamp: String

    enum CodingKeys: String, CodingKey {
        case requestId = "request_id"
        case timestamp
    }

    public init(requestId: String = "", timestamp: String = "") {
        self.requestId = requestId
        self.timestamp = timestamp
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.requestId = try container.decodeIfPresent(String.self, forKey: .requestId) ?? ""
        self.timestamp = try container.decodeIfPresent(String.self, forKey: .timestamp) ?? ""
    }
}

public struct APIErrorBody: Decodable, Sendable {
    public let code: String
    public let message: String

    enum CodingKeys: String, CodingKey {
        case code, message
    }

    public init(from decoder: any Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.code = (try? container.decodeIfPresent(String.self, forKey: .code)) ?? "unknown"
        self.message = (try? container.decodeIfPresent(String.self, forKey: .message)) ?? ""
    }
}
