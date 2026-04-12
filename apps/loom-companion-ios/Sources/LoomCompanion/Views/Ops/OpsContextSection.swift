import SwiftUI
import LoomCompanionKit

/// Context section: memory, stream, graph, reasoning chains.
struct OpsContextSection: View {
    @Bindable var viewModel: OpsViewModel
    @State private var streamDisplayLimit = 8
    @State private var chainDisplayLimit = 8

    var body: some View {
        VStack(spacing: LoomSpacing.cardSpacing) {
            memoryCard
                .cardAppear(index: 0)

            streamCard
                .cardAppear(index: 1)

            graphReasoningCard
                .cardAppear(index: 2)

            Text("Read-only in Wave 1.")
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textTertiary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task {
            await viewModel.loadSectionIfNeeded(.context)
        }
    }

    // MARK: - Memory Card

    private var memoryCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Memory")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                if let stats = viewModel.memoryStats {
                    #if canImport(Charts)
                    MemoryTierChart(stats: stats)
                    #endif

                    HStack {
                        opsMetric(label: "Items", value: stats.totalItems, icon: "brain.head.profile.fill", color: LoomColors.accent)
                        Spacer()
                        opsMetric(label: "Tokens", value: stats.totalTokens, icon: "text.word.spacing", color: LoomColors.statusInfo)
                    }

                    HStack(spacing: LoomSpacing.lg) {
                        Label("Working \(stats.workingMemory.items)", systemImage: "bolt.fill")
                        Label("Short \(stats.shortTermMemory.items)", systemImage: "clock.fill")
                        Label("Long \(stats.longTermMemory.items)", systemImage: "archivebox.fill")
                    }
                    .font(LoomTypography.caption)
                    .foregroundStyle(LoomColors.textSecondary)
                } else {
                    Text("Memory stats unavailable")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textTertiary)
                }
            }
        }
    }

    // MARK: - Stream Card

    private var streamCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Stream")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                if viewModel.streamEntries.isEmpty {
                    Text("No stream entries")
                        .font(LoomTypography.bodyRegular)
                        .foregroundStyle(LoomColors.textTertiary)
                } else {
                    ForEach(Array(viewModel.streamEntries.prefix(streamDisplayLimit).enumerated()), id: \.element.id) { index, entry in
                        HStack(spacing: LoomSpacing.sm) {
                            Image(systemName: streamEntryIcon(entry.entryType))
                                .foregroundStyle(LoomColors.accent)
                                .symbolEffect(.bounce, value: entry.id)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(entry.title)
                                    .font(LoomTypography.bodyMedium)
                                    .foregroundStyle(LoomColors.textPrimary)
                                    .lineLimit(1)
                                Text("\(entry.entryType) \u{2022} \(entry.agentId)")
                                    .font(LoomTypography.caption)
                                    .foregroundStyle(LoomColors.textSecondary)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .padding(.vertical, 2)
                        .cardAppear(index: index)
                    }
                    if viewModel.streamEntries.count > streamDisplayLimit {
                        Button {
                            withAnimation(.easeInOut(duration: 0.25)) {
                                streamDisplayLimit += 8
                            }
                            HapticManager.light()
                        } label: {
                            Text("Show \(min(8, viewModel.streamEntries.count - streamDisplayLimit)) More")
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.accent)
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 6)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Graph & Reasoning Card

    private var graphReasoningCard: some View {
        LoomCard {
            VStack(alignment: .leading, spacing: LoomSpacing.sm) {
                Text("Graph & Reasoning")
                    .font(LoomTypography.headlineMedium)
                    .foregroundStyle(LoomColors.textPrimary)

                if let stats = viewModel.graphStats {
                    HStack {
                        opsMetric(label: "Entities", value: stats.totalEntities, icon: "circle.hexagongrid.fill", color: LoomColors.accent)
                        Spacer()
                        opsMetric(label: "Relations", value: stats.totalRelations, icon: "arrow.triangle.branch", color: LoomColors.statusInfo)
                    }
                }
                if let path = viewModel.graphPath {
                    Label("Sample path length: \(path.length)", systemImage: "point.3.connected.trianglepath.dotted")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textSecondary)
                }
                if viewModel.reasoningChains.isEmpty {
                    Text("Reasoning chains: 0")
                        .font(LoomTypography.caption)
                        .foregroundStyle(LoomColors.textTertiary)
                } else {
                    ForEach(Array(viewModel.reasoningChains.prefix(chainDisplayLimit))) { chain in
                        NavigationLink {
                            OpsReasoningChainDetailView(
                                chain: chain,
                                loadDetail: viewModel.loadReasoningChainDetail(id:)
                            )
                        } label: {
                            HStack(spacing: LoomSpacing.sm) {
                                StatusAccentBar(color: chainStatusColor(chain.status))
                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text(chain.title)
                                            .font(LoomTypography.bodyMedium)
                                            .foregroundStyle(LoomColors.textPrimary)
                                            .lineLimit(1)
                                        StatusBadge(
                                            "\(chain.stepCount) steps",
                                            color: chainStatusColor(chain.status)
                                        )
                                    }
                                    Text(chain.status.rawValue)
                                        .font(LoomTypography.caption)
                                        .foregroundStyle(LoomColors.textSecondary)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                            }
                            .padding(.vertical, 2)
                        }
                    }
                    if viewModel.reasoningChains.count > chainDisplayLimit {
                        Button {
                            withAnimation(.easeInOut(duration: 0.25)) {
                                chainDisplayLimit += 8
                            }
                            HapticManager.light()
                        } label: {
                            Text("Show \(min(8, viewModel.reasoningChains.count - chainDisplayLimit)) More")
                                .font(LoomTypography.caption)
                                .foregroundStyle(LoomColors.accent)
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 6)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Helpers

    private func opsMetric(label: String, value: Int, icon: String, color: Color) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 4) {
                Image(systemName: icon)
                    .font(.caption2)
                    .foregroundStyle(color)
                AnimatedCounter(value)
                    .font(LoomTypography.counterSmall)
                    .foregroundStyle(LoomColors.textPrimary)
            }
            Text(label)
                .font(LoomTypography.caption)
                .foregroundStyle(LoomColors.textSecondary)
        }
    }

    private func chainStatusColor(_ status: MobileReasoningStatus) -> Color {
        switch status {
        case .active: return LoomColors.statusActive
        case .completed: return LoomColors.statusHealthy
        case .abandoned: return LoomColors.statusCritical
        case .unknown: return LoomColors.statusIdle
        }
    }

    private func streamEntryIcon(_ entryType: String) -> String {
        switch entryType {
        case "decision": return "lightbulb.fill"
        case "observation": return "eye.fill"
        case "progress": return "arrow.right.circle.fill"
        case "error": return "exclamationmark.triangle.fill"
        case "context": return "doc.text.fill"
        default: return "circle.fill"
        }
    }
}
