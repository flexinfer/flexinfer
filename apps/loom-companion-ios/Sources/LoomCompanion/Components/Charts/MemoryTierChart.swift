import SwiftUI

#if canImport(Charts)
import Charts
import LoomCompanionKit

struct MemoryTierChart: View {
    let stats: MobileMemoryStats

    private struct TierData: Identifiable {
        let id: String
        let label: String
        let tokens: Int
        let items: Int
        let color: Color
    }

    private var tiers: [TierData] {
        [
            TierData(id: "working", label: "Working", tokens: stats.workingMemory.tokens, items: stats.workingMemory.items, color: LoomColors.tierWorking),
            TierData(id: "short", label: "Short-term", tokens: stats.shortTermMemory.tokens, items: stats.shortTermMemory.items, color: LoomColors.tierShortTerm),
            TierData(id: "long", label: "Long-term", tokens: stats.longTermMemory.tokens, items: stats.longTermMemory.items, color: LoomColors.tierLongTerm),
        ]
    }

    var body: some View {
        Chart(tiers) { tier in
            BarMark(
                x: .value("Tier", tier.label),
                y: .value("Tokens", tier.tokens)
            )
            .foregroundStyle(tier.color)
            .cornerRadius(4)
            .annotation(position: .top, alignment: .center) {
                Text("\(tier.items)")
                    .font(LoomTypography.monoCaption)
                    .foregroundStyle(LoomColors.textSecondary)
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading) { _ in
                AxisGridLine(stroke: .init(lineWidth: 0.5, dash: [4]))
                    .foregroundStyle(LoomColors.textTertiary)
                AxisValueLabel()
                    .font(LoomTypography.monoCaption)
            }
        }
        .chartXAxis {
            AxisMarks { _ in
                AxisValueLabel()
                    .font(LoomTypography.monoCaption)
            }
        }
        .frame(height: 140)
    }
}
#endif
