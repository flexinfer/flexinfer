import SwiftUI

#if canImport(Charts)
import Charts
import LoomCompanionKit

struct SessionTimelineChart: View {
    let entries: [TimelineEntry]

    private struct Bucket: Identifiable {
        let id: Int
        let label: String
        let count: Int
    }

    private var buckets: [Bucket] {
        guard entries.count > 1 else {
            return [Bucket(id: 0, label: "Now", count: entries.count)]
        }
        let bucketCount = min(entries.count, 8)
        let chunkSize = max(1, entries.count / bucketCount)
        return stride(from: 0, to: entries.count, by: chunkSize).enumerated().map { index, start in
            let end = min(start + chunkSize, entries.count)
            return Bucket(id: index, label: "\(index + 1)", count: end - start)
        }
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
        .chartXAxis(.hidden)
        .chartYAxis(.hidden)
        .frame(height: 60)
    }
}
#endif
