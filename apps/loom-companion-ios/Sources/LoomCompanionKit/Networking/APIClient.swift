import Foundation

/// Protocol for testability — ViewModels depend on this, not the concrete client.
public protocol LoomAPIClientProtocol: Sendable {
    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T
}

/// URLSession-based REST client for the Loom mobile v1 API.
public final class APIClient: LoomAPIClientProtocol, Sendable {
    private let session: URLSession
    private let baseURL: URL
    private let token: String
    private let deviceId: String

    public init(baseURL: URL, token: String, deviceId: String = "", session: URLSession? = nil) {
        self.baseURL = baseURL
        self.token = token
        self.deviceId = deviceId

        if let session {
            self.session = session
        } else {
            let config = URLSessionConfiguration.default
            config.timeoutIntervalForRequest = 15
            config.timeoutIntervalForResource = 30
            self.session = URLSession(configuration: config)
        }
    }

    public func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        var urlRequest = try endpoint.urlRequest(baseURL: baseURL)
        urlRequest.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if !deviceId.isEmpty {
            urlRequest.setValue(deviceId, forHTTPHeaderField: "X-Device-ID")
        }

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: urlRequest)
        } catch {
            throw LoomAPIError.networkError(underlying: error.localizedDescription)
        }

        guard let httpResponse = response as? HTTPURLResponse else {
            throw LoomAPIError.networkError(underlying: "Non-HTTP response")
        }

        let envelope: APIEnvelope<T>
        do {
            envelope = try JSONDecoder().decode(APIEnvelope<T>.self, from: data)
        } catch {
            // If we can't decode the envelope at all, map HTTP status to error
            throw mapHTTPError(status: httpResponse.statusCode, data: data)
        }

        guard envelope.ok, let result = envelope.data else {
            let errorCode = APIErrorCode(rawValue: envelope.error?.code ?? "") ?? .unknown
            throw LoomAPIError.apiError(
                code: errorCode,
                message: envelope.error?.message ?? "Unknown error",
                requestId: envelope.meta.requestId
            )
        }

        return result
    }

    /// Build an SSE request (used by SSEClient).
    public func sseRequest() throws -> URLRequest {
        var request = try Endpoint.eventsStream.urlRequest(baseURL: baseURL)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if !deviceId.isEmpty {
            request.setValue(deviceId, forHTTPHeaderField: "X-Device-ID")
        }
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        request.timeoutInterval = 0 // No timeout for SSE
        return request
    }

    private func mapHTTPError(status: Int, data: Data) -> LoomAPIError {
        // Try to parse just the error body
        if let envelope = try? JSONDecoder().decode(APIEnvelope<EmptyData>.self, from: data),
           let error = envelope.error
        {
            let code = APIErrorCode(rawValue: error.code) ?? .unknown
            return .apiError(code: code, message: error.message, requestId: envelope.meta.requestId)
        }

        // Fallback to HTTP status
        switch status {
        case 401: return .apiError(code: .unauthorized, message: "Unauthorized", requestId: "")
        case 403: return .apiError(code: .forbidden, message: "Forbidden", requestId: "")
        case 429: return .apiError(code: .rateLimited, message: "Rate limited", requestId: "")
        default: return .networkError(underlying: "HTTP \(status)")
        }
    }
}

/// Empty decodable for error-only envelopes.
private struct EmptyData: Decodable {}
