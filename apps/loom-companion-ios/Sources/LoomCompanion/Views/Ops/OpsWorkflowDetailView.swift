import SwiftUI
import LoomCompanionKit

struct OpsWorkflowDetailView: View {
    let workflow: MobileWorkflow
    let loadDetail: (String) async throws -> MobileWorkflowDetailResponse

    @State private var detail: MobileWorkflowDetail?
    @State private var events: [MobileWorkflowEvent] = []
    @State private var isLoading = false
    @State private var error: String?

    var body: some View {
        List {
            Section("Overview") {
                LabeledContent("Name", value: workflow.name ?? workflow.id)
                LabeledContent("Status") {
                    StatusBadge(workflow.status.rawValue, color: statusColor(workflow.status))
                }
                if let step = detail?.currentStep ?? workflow.currentStep {
                    LabeledContent("Current Step", value: step)
                }
                LabeledContent("Progress") {
                    ProgressView(value: detail?.progress ?? workflow.progress)
                        .frame(width: 100)
                }
                LabeledContent("Started", value: workflow.startedAt)
                if let completed = workflow.completedAt {
                    LabeledContent("Completed", value: completed)
                }
                if let err = workflow.error {
                    LabeledContent("Error") {
                        Text(err)
                            .foregroundStyle(LoomColors.statusCritical)
                    }
                }
            }

            if let detail, let steps = detail.steps, !steps.isEmpty {
                Section("Steps") {
                    ForEach(steps) { step in
                        HStack(spacing: LoomSpacing.sm) {
                            Image(systemName: stepIcon(step.status))
                                .foregroundStyle(statusColor(step.status))
                            VStack(alignment: .leading, spacing: 2) {
                                Text(step.name)
                                    .font(LoomTypography.bodyMedium)
                                    .foregroundStyle(LoomColors.textPrimary)
                                if let type = step.type {
                                    Text(type)
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.textSecondary)
                                }
                            }
                            Spacer()
                            Text(step.status.rawValue)
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.textTertiary)
                        }
                    }
                }
            }

            if !events.isEmpty {
                Section("Events") {
                    ForEach(events) { event in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(event.eventType)
                                    .font(LoomTypography.bodyMedium)
                                    .foregroundStyle(LoomColors.textPrimary)
                                Spacer()
                                Text(event.timestamp)
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textTertiary)
                            }
                            if let stepName = event.stepName {
                                Text(stepName)
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textSecondary)
                            }
                            if let details = event.details {
                                Text(details)
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textSecondary)
                            }
                        }
                    }
                }
            }

            if let error {
                Section {
                    Text(error)
                        .foregroundStyle(.red)
                        .font(LoomTypography.caption)
                }
            }
        }
        .navigationTitle("Workflow")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await loadWorkflowDetail()
        }
        .refreshable {
            await loadWorkflowDetail()
        }
    }

    private func loadWorkflowDetail() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let response = try await loadDetail(workflow.id)
            detail = response.workflow
            events = response.events
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func statusColor(_ status: MobileWorkflowStatus) -> Color {
        switch status {
        case .running: return LoomColors.statusActive
        case .completed: return LoomColors.statusHealthy
        case .failed: return LoomColors.statusCritical
        case .waitingApproval: return LoomColors.statusDegraded
        case .cancelled, .unknown: return LoomColors.statusIdle
        }
    }

    private func stepIcon(_ status: MobileWorkflowStatus) -> String {
        switch status {
        case .running: return "play.circle.fill"
        case .completed: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        case .waitingApproval: return "pause.circle.fill"
        case .cancelled: return "minus.circle.fill"
        case .unknown: return "circle"
        }
    }
}
