import Foundation

/// Standard response envelope matching `mobileEnvelope` from api_mobile.go.
public struct APIEnvelope<T: Decodable>: Decodable {
    public let ok: Bool
    public let data: T?
    public let error: APIErrorBody?
    public let meta: APIMeta
}

public struct APIMeta: Decodable, Sendable {
    public let requestId: String
    public let timestamp: String

    enum CodingKeys: String, CodingKey {
        case requestId = "request_id"
        case timestamp
    }
}

public struct APIErrorBody: Decodable, Sendable {
    public let code: String
    public let message: String
}
