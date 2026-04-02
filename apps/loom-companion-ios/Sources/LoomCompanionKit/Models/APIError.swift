import Foundation

/// Error codes returned by the mobile v1 API.
public enum APIErrorCode: String, Sendable {
    case unauthorized
    case tokenRevoked = "token_revoked"
    case forbidden
    case rateLimited = "rate_limited"
    case notConfigured = "not_configured"
    case upstreamError = "upstream_error"
    case notFound = "not_found"
    case badRequest = "bad_request"
    case unknown
}

/// Typed error for Loom mobile API operations.
public enum LoomAPIError: Error, Sendable {
    case apiError(code: APIErrorCode, message: String, requestId: String)
    case networkError(underlying: String)
    case decodingError(underlying: String)
    case invalidURL(url: String)
    case noToken
}

extension LoomAPIError: CustomStringConvertible {
    public var description: String {
        switch self {
        case let .apiError(code, message, requestId):
            return "[\(code.rawValue)] \(message) (req: \(requestId))"
        case let .networkError(underlying):
            return "Network error: \(underlying)"
        case let .decodingError(underlying):
            return "Decoding error: \(underlying)"
        case let .invalidURL(url):
            return "Invalid URL: \(url)"
        case .noToken:
            return "No bearer token configured"
        }
    }
}

extension LoomAPIError {
    /// Whether this error indicates an authentication problem.
    public var isAuthError: Bool {
        switch self {
        case let .apiError(code, _, _):
            return code == .unauthorized || code == .tokenRevoked
        default:
            return false
        }
    }

    /// Whether this error indicates a rate limit.
    public var isRateLimited: Bool {
        switch self {
        case let .apiError(code, _, _):
            return code == .rateLimited
        default:
            return false
        }
    }

    public var dashboardTitle: String {
        switch self {
        case .noToken:
            return "Reconnect Required"
        case .invalidURL:
            return "Invalid Server URL"
        case .networkError:
            return "Server Unreachable"
        case .decodingError:
            return "Unexpected Server Response"
        case let .apiError(code, _, _):
            switch code {
            case .unauthorized, .tokenRevoked:
                return "Authentication Failed"
            case .forbidden:
                return "Permission Denied"
            case .notFound:
                return "Mobile Route Missing"
            case .rateLimited:
                return "Rate Limited"
            case .notConfigured:
                return "Server Not Configured"
            case .upstreamError:
                return "Upstream Service Error"
            case .badRequest, .unknown:
                return "Request Failed"
            }
        }
    }

    public var shouldSuggestConnectionTab: Bool {
        switch self {
        case .noToken, .invalidURL, .networkError:
            return true
        case let .apiError(code, _, _):
            return code == .unauthorized || code == .tokenRevoked || code == .notFound
        case .decodingError:
            return false
        }
    }
}
