import SwiftUI

#if canImport(Charts)
import Charts
import LoomCompanionKit

struct SessionTimelineChart: View {
    let entries: [TimelineEntry]

    private static let isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    private static let isoFallback: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private struct TimeBucket: Identifiable {
        let id: Int
        let date: Date
        let count: Int
    }

    private func parseDate(_ s: String) -> Date? {
        Self.isoFormatter.date(from: s) ?? Self.isoFallback.date(from: s)
    }

    private var buckets: [TimeBucket] {
        let dates = entries.compactMap { parseDate($0.timestamp) }.sorted()
        guard dates.count > 1, let earliest = dates.first, let latest = dates.last else {
            return [TimeBucket(id: 0, date: Date(), count: entries.count)]
        }

        let span = latest.timeIntervalSince(earliest)
        let bucketCount = min(max(dates.count, 4), 8)
        let interval = span / Double(bucketCount)
        guard interval > 0 else {
            return [TimeBucket(id: 0, date: earliest, count: dates.count)]
        }

        var result: [TimeBucket] = []
        for i in 0..<bucketCount {
            let start = earliest.addingTimeInterval(Double(i) * interval)
            let end = earliest.addingTimeInterval(Double(i + 1) * interval)
            let count = dates.filter { $0 >= start && (i == bucketCount - 1 ? $0 <= end : $0 < end) }.count
            result.append(TimeBucket(id: i, date: start, count: count))
        }
        return result
    }

    private func relativeLabel(_ date: Date) -> String {
        let diff = Int(Date().timeIntervalSince(date))
        if diff < 60 { return "now" }
        if diff < 3600 { return "\(diff / 60)m" }
        return "\(diff / 3600)h"
    }

    var body: some View {
        Chart(buckets) { bucket in
            LineMark(
                x: .value("Time", bucket.id),
                y: .value("Events", bucket.count)
            )
            .foregroundStyle(LoomColors.accent)
            .interpolationMethod(.catmullRom)
            .lineStyle(StrokeStyle(lineWidth: 2))

            AreaMark(
                x: .value("Time", bucket.id),
                y: .value("Events", bucket.count)
            )
            .foregroundStyle(
                LinearGradient(
                    colors: [LoomColors.accent.opacity(0.3), LoomColors.accent.opacity(0.05)],
                    startPoint: .top,
                    endPoint: .bottom
                )
            )
            .interpolationMethod(.catmullRom)
        }
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 3)) { value in
                if let idx = value.as(Int.self), idx < buckets.count {
                    AxisValueLabel {
                        Text(relativeLabel(buckets[idx].date))
                            .font(LoomTypography.monoCaption)
                    }
                }
            }
        }
        .chartYAxis(.hidden)
        .frame(height: 60)
    }
}
#endif
