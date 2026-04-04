import SwiftUI
import LoomCompanionKit

struct OpsReasoningChainDetailView: View {
    let chain: MobileReasoningChain
    let loadDetail: (String) async throws -> MobileReasoningChainDetailResponse

    @State private var detail: MobileReasoningChain?
    @State private var isLoading = false
    @State private var error: String?

    private var displayChain: MobileReasoningChain {
        detail ?? chain
    }

    var body: some View {
        List {
            Section("Overview") {
                LabeledContent("Title", value: displayChain.title)
                LabeledContent("Status") {
                    StatusBadge(displayChain.status.rawValue, color: statusColor)
                }
                LabeledContent("Steps", value: "\(displayChain.stepCount)")
                if let confidence = displayChain.confidence {
                    LabeledContent("Confidence") {
                        Text(String(format: "%.0f%%", confidence * 100))
                            .foregroundStyle(confidence > 0.7 ? LoomColors.statusHealthy : LoomColors.statusDegraded)
                    }
                }
                LabeledContent("Created", value: displayChain.createdAt)
                if let completed = displayChain.completedAt {
                    LabeledContent("Completed", value: completed)
                }
            }

            if let steps = displayChain.steps, !steps.isEmpty {
                Section("Reasoning Steps") {
                    ForEach(Array(steps.enumerated()), id: \.element.id) { index, step in
                        VStack(alignment: .leading, spacing: LoomSpacing.xs) {
                            HStack {
                                Text("Step \(index + 1)")
                                    .font(LoomTypography.labelSmall)
                                    .foregroundStyle(LoomColors.accent)
                                Spacer()
                                Text(String(format: "%.0f%%", step.confidence * 100))
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(step.confidence > 0.7 ? LoomColors.statusHealthy : LoomColors.statusDegraded)
                            }

                            Text(step.description)
                                .font(LoomTypography.bodyRegular)
                                .foregroundStyle(LoomColors.textPrimary)
                                .textSelection(.enabled)

                            if let evidence = step.evidence, !evidence.isEmpty {
                                Text(evidence)
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textSecondary)
                                    .textSelection(.enabled)
                            }

                            Text(step.createdAt)
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.textTertiary)
                        }
                        .padding(.vertical, 2)
                    }
                }
            }

            if let error {
                Section {
                    Text(error)
                        .foregroundStyle(LoomColors.statusCritical)
                        .font(LoomTypography.caption)
                }
            }
        }
        .navigationTitle("Reasoning Chain")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            await loadChainDetail()
        }
        .refreshable {
            await loadChainDetail()
        }
    }

    private func loadChainDetail() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let response = try await loadDetail(chain.id)
            detail = response.chain
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private var statusColor: Color {
        switch displayChain.status {
        case .active: return LoomColors.statusActive
        case .completed: return LoomColors.statusHealthy
        case .abandoned: return LoomColors.statusCritical
        case .unknown: return LoomColors.statusIdle
        }
    }
}
