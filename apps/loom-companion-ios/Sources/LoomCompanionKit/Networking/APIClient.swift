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
    private let cloudflareAccessClientID: String?
    private let cloudflareAccessClientSecret: String?

    /// Delegate that accepts self-signed TLS certificates for LAN mode.
    /// Must be retained for the lifetime of the URLSession.
    private let sessionDelegate: InsecureTLSDelegate?

    public init(
        baseURL: URL,
        token: String,
        deviceId: String = "",
        cloudflareAccessClientID: String? = nil,
        cloudflareAccessClientSecret: String? = nil,
        allowsInsecureTLS: Bool = false,
        session: URLSession? = nil
    ) {
        self.baseURL = baseURL
        self.token = token
        self.deviceId = deviceId
        self.cloudflareAccessClientID = cloudflareAccessClientID?.trimmingCharacters(in: .whitespacesAndNewlines)
        self.cloudflareAccessClientSecret = cloudflareAccessClientSecret?.trimmingCharacters(in: .whitespacesAndNewlines)

        if let session {
            self.session = session
            self.sessionDelegate = nil
        } else {
            let config = URLSessionConfiguration.default
            config.timeoutIntervalForRequest = 15
            config.timeoutIntervalForResource = 30
            if allowsInsecureTLS {
                let delegate = InsecureTLSDelegate()
                self.sessionDelegate = delegate
                self.session = URLSession(configuration: config, delegate: delegate, delegateQueue: nil)
            } else {
                self.sessionDelegate = nil
                self.session = URLSession(configuration: config)
            }
        }
    }

    public func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        var urlRequest = try endpoint.urlRequest(baseURL: baseURL)
        applyAuthHeaders(to: &urlRequest)

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

        return try decodeResponse(data, statusCode: httpResponse.statusCode)
    }

    /// Decode and validate the standard mobile API envelope contract.
    ///
    /// Contract expectations:
    /// - Body is an `APIEnvelope<T>`
    /// - `ok == false` carries structured `error`
    /// - `ok == true` includes non-nil `data`
    /// - 2xx with invalid/missing envelope data is a decoding contract error
    func decodeResponse<T: Decodable>(_ data: Data, statusCode: Int) throws -> T {
        let envelope: APIEnvelope<T>
        do {
            envelope = try JSONDecoder().decode(APIEnvelope<T>.self, from: data)
        } catch {
            // If we can't decode a successful payload, surface a contract error
            // with the actual decode error for diagnostics.
            if (200 ..< 300).contains(statusCode) {
                throw LoomAPIError.decodingError(underlying: "Invalid API response contract (HTTP \(statusCode)): \(error.localizedDescription)")
            }
            // For non-2xx responses, map HTTP status to structured API errors.
            throw mapHTTPError(status: statusCode, data: data)
        }

        if !envelope.ok {
            let errorCode = APIErrorCode(rawValue: envelope.error?.code ?? "") ?? .unknown
            throw LoomAPIError.apiError(
                code: errorCode,
                message: envelope.error?.message ?? "Unknown error",
                requestId: envelope.meta.requestId
            )
        }

        guard let result = envelope.data else {
            throw LoomAPIError.decodingError(underlying: "Missing data payload in successful API response (HTTP \(statusCode))")
        }

        return result
    }

    /// Build an SSE request (used by SSEClient).
    public func sseRequest() throws -> URLRequest {
        var request = try Endpoint.eventsStream.urlRequest(baseURL: baseURL)
        applyAuthHeaders(to: &request)
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        request.timeoutInterval = 0 // No timeout for SSE
        return request
    }

    /// Build a URLSession suitable for SSE streaming (no request timeout).
    /// Inherits TLS trust settings from this client.
    public func sseSession() -> URLSession {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 0
        config.timeoutIntervalForResource = 0
        if let delegate = sessionDelegate {
            return URLSession(configuration: config, delegate: delegate, delegateQueue: nil)
        }
        return URLSession(configuration: config)
    }

    private func applyAuthHeaders(to request: inout URLRequest) {
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if !deviceId.isEmpty {
            request.setValue(deviceId, forHTTPHeaderField: "X-Device-ID")
        }
        if let id = cloudflareAccessClientID,
           let secret = cloudflareAccessClientSecret,
           !id.isEmpty,
           !secret.isEmpty
        {
            request.setValue(id, forHTTPHeaderField: "CF-Access-Client-Id")
            request.setValue(secret, forHTTPHeaderField: "CF-Access-Client-Secret")
        }
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
        case 400: return .apiError(code: .badRequest, message: "Bad request", requestId: "")
        case 401: return .apiError(code: .unauthorized, message: "Unauthorized", requestId: "")
        case 403: return .apiError(code: .forbidden, message: "Forbidden", requestId: "")
        case 404: return .apiError(code: .notFound, message: "Not found", requestId: "")
        case 429: return .apiError(code: .rateLimited, message: "Rate limited", requestId: "")
        case 500, 502, 503, 504:
            return .apiError(code: .upstreamError, message: "Upstream service error (HTTP \(status))", requestId: "")
        default: return .networkError(underlying: "HTTP \(status)")
        }
    }
}

/// Empty decodable for error-only envelopes.
private struct EmptyData: Decodable {}

/// URLSession delegate that accepts self-signed TLS certificates.
/// Used in LAN mode where the HUD serves HTTPS with a self-signed cert.
final class InsecureTLSDelegate: NSObject, URLSessionDelegate, Sendable {
    func urlSession(
        _ session: URLSession,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
              let serverTrust = challenge.protectionSpace.serverTrust
        else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(trust: serverTrust))
    }
}
