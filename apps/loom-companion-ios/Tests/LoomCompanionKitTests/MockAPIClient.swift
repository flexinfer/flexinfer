import Foundation
@testable import LoomCompanionKit

/// Mock API client for ViewModel tests.
final class MockAPIClient: LoomAPIClientProtocol, @unchecked Sendable {
    var shouldFail = false
    var failError: LoomAPIError = .apiError(code: .unauthorized, message: "mock error", requestId: "mock")

    var dashboardResponse: DashboardData?
    var sessionsResponse: SessionsResponse?
    var sessionDetailResponse: SessionDetailResponse?
    var sessionEventsResponse: SessionEventsResponse?

    func request<T: Decodable>(_ endpoint: Endpoint) async throws -> T {
        if shouldFail {
            throw failError
        }

        switch endpoint {
        case .dashboard:
            if let r = dashboardResponse as? T { return r }
        case .sessions:
            if let r = sessionsResponse as? T { return r }
        case .sessionDetail:
            if let r = sessionDetailResponse as? T { return r }
        case .sessionEvents:
            if let r = sessionEventsResponse as? T { return r }
        default:
            break
        }

        throw LoomAPIError.noToken
    }
}
