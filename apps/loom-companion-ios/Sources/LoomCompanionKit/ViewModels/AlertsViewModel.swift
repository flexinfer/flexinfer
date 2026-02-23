import Foundation

/// ViewModel for the in-app alert inbox. Accumulates alerts from SSE events.
@Observable
public final class AlertsViewModel {
    public private(set) var alerts: [AlertItem] = []

    /// Maximum number of alerts retained; oldest are evicted beyond this limit.
    public static let maxAlerts = 100

    public var unreadCount: Int {
        alerts.filter { !$0.isRead }.count
    }

    public var criticalAlerts: [AlertItem] {
        alerts.filter { $0.severity == .critical && !$0.isRead }
    }

    public init() {}

    /// Classify an SSE event via NotificationPolicy and prepend to the alert list.
    public func handleSSEEvent(_ event: SSEEvent) {
        guard let alert = NotificationPolicy.classify(event: event) else { return }
        alerts.insert(alert, at: 0)
        if alerts.count > Self.maxAlerts {
            alerts = Array(alerts.prefix(Self.maxAlerts))
        }
    }

    /// Mark a single alert as read.
    public func markRead(_ id: UUID) {
        guard let index = alerts.firstIndex(where: { $0.id == id }) else { return }
        alerts[index].isRead = true
    }

    /// Mark all alerts as read.
    public func markAllRead() {
        for i in alerts.indices {
            alerts[i].isRead = true
        }
    }

    /// Remove all alerts.
    public func clearAll() {
        alerts.removeAll()
    }
}
