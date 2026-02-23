import SwiftUI
import LoomCompanionKit

struct SessionEventsView: View {
    let events: [TimelineEntry]

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Events")
                    .font(.headline)
                Spacer()
                Text("\(events.count)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            if events.isEmpty {
                Text("No events recorded")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 8)
            } else {
                ForEach(events) { entry in
                    EventRow(entry: entry)
                    if entry.id != events.last?.id {
                        Divider()
                    }
                }
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

private struct EventRow: View {
    let entry: TimelineEntry
    @State private var isExpanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Button {
                withAnimation { isExpanded.toggle() }
            } label: {
                HStack {
                    Text(entry.eventType)
                        .font(.caption)
                        .fontWeight(.medium)
                        .monospaced()

                    Spacer()

                    if let agentId = entry.agentId {
                        Text(agentId)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }

                    Text(entry.timestamp)
                        .font(.caption2)
                        .foregroundStyle(.tertiary)

                    if entry.data != nil {
                        Image(systemName: isExpanded ? "chevron.up" : "chevron.down")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            .buttonStyle(.plain)

            if isExpanded, let data = entry.data {
                Text(formatData(data))
                    .font(.caption2)
                    .monospaced()
                    .foregroundStyle(.secondary)
                    .padding(8)
                    .background(.quaternary)
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            }
        }
    }

    private func formatData(_ data: [String: AnyCodable]) -> String {
        guard let jsonData = try? JSONEncoder().encode(data),
              let jsonObject = try? JSONSerialization.jsonObject(with: jsonData),
              let prettyData = try? JSONSerialization.data(withJSONObject: jsonObject, options: .prettyPrinted),
              let prettyString = String(data: prettyData, encoding: .utf8)
        else {
            return "{...}"
        }
        return prettyString
    }
}
