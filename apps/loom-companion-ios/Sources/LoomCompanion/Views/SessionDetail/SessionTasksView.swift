import SwiftUI
import LoomCompanionKit

struct SessionTasksView: View {
    let tasks: SessionTaskSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("Tasks", systemImage: "checklist")
                    .font(.headline)
                Spacer()
                Text("\(tasks.total)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 16) {
                TaskCountBadge(label: "Pending", count: tasks.pending, color: .orange)
                TaskCountBadge(label: "In Progress", count: tasks.inProgress, color: .blue)
                TaskCountBadge(label: "Done", count: tasks.completed, color: .green)
            }
        }
        .padding()
        .background(.regularMaterial)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

private struct TaskCountBadge: View {
    let label: String
    let count: Int
    let color: Color

    var body: some View {
        VStack(spacing: 4) {
            Text("\(count)")
                .font(.title2)
                .fontWeight(.semibold)
                .foregroundStyle(count > 0 ? color : .secondary)
            Text(label)
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }
}
