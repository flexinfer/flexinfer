// MillsScreen — Phase 7 slice 7.5.
//
// Three KPI cards across the top, in-flight pipelines list below. Pull-
// to-refresh hooked into both fetches. Read-only — no mutations land
// from this screen. The MillsAPI is injected so previews + tests can
// short-circuit network calls.

import LoomCompanionKit
import SwiftUI

public struct MillsScreen: View {
    @State private var pipelineRuns: [MillsPipelineRun] = []
    @State private var kpi: MillsKPISnapshot?
    @State private var loading = true
    @State private var loadError: String?

    private let api: MillsAPIProtocol?

    /// Initializer accepting an optional concrete API. When the user
    /// hasn't paired with a HUD yet, `connectionVM.buildAPIClient()`
    /// returns nil; the screen renders a "connect to view Mills" empty
    /// state in that case.
    public init(apiClient: APIClient?) {
        self.api = apiClient.map(MillsAPI.init(client:))
    }

    /// Test-only initializer that accepts a fake protocol implementation.
    public init(api: MillsAPIProtocol?) {
        self.api = api
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                kpiRow
                Text("In-flight pipelines")
                    .font(.headline)
                    .padding(.horizontal)
                pipelinesList
            }
            .padding(.vertical)
        }
        .navigationTitle("Mills")
        .refreshable { await reload() }
        .task { await reload() }
    }

    // MARK: KPI row

    private var kpiRow: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 12) {
                kpiCard(
                    title: "Merge rate",
                    value: kpi.flatMap { fmtPercent($0.metrics["pipeline_merge_rate"]) } ?? "—"
                )
                kpiCard(
                    title: "Cost / day",
                    value: kpi.flatMap { fmtUSD($0.metrics["council_cost_per_day_usd"]) } ?? "—"
                )
                kpiCard(
                    title: "Audits",
                    value: kpi.flatMap { fmtCount($0.metrics["audit_findings_count"]) } ?? "—"
                )
            }
            .padding(.horizontal)
        }
    }

    private func kpiCard(title: String, value: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title.uppercased())
                .font(.caption2)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title2.weight(.semibold))
                .monospacedDigit()
            if kpi == nil {
                Text("no data yet")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(12)
        .frame(minWidth: 140, alignment: .leading)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 12))
    }

    // MARK: Pipelines list

    private var pipelinesList: some View {
        Group {
            if loading && pipelineRuns.isEmpty {
                ProgressView().padding()
            } else if let err = loadError {
                emptyState(icon: "exclamationmark.triangle", message: "Failed to load", hint: err)
            } else if inFlight.isEmpty {
                emptyState(icon: "checkmark.circle", message: "No pipelines running", hint: "Idle mills — new work will land here.")
            } else {
                LazyVStack(alignment: .leading, spacing: 8) {
                    ForEach(inFlight) { run in
                        pipelineRow(run)
                    }
                }
                .padding(.horizontal)
            }
        }
    }

    /// `inFlight` filters out terminal states so the list stays focused
    /// on what's actually running. Mirror the HUD's PipelinesPanel
    /// terminal-state set so the two views agree.
    private var inFlight: [MillsPipelineRun] {
        pipelineRuns.filter { !["done", "merged", "escalated", "failed"].contains($0.state) }
    }

    private func pipelineRow(_ run: MillsPipelineRun) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            depthPill(run)
            VStack(alignment: .leading, spacing: 2) {
                Text(run.id)
                    .font(.caption.monospaced())
                Text(run.state)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Text(run.template)
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
        .padding(10)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 10))
    }

    private func depthPill(_ run: MillsPipelineRun) -> some View {
        let depth = run.depth ?? 0
        let label = depth == 0 ? "root" : "d\(depth)"
        return Text(label)
            .font(.caption2.monospaced().weight(.semibold))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(depth == 0 ? Color.secondary.opacity(0.15) : Color.accentColor.opacity(0.18))
            .foregroundStyle(depth == 0 ? Color.secondary : Color.accentColor)
            .clipShape(Capsule())
    }

    private func emptyState(icon: String, message: String, hint: String) -> some View {
        VStack(spacing: 8) {
            Image(systemName: icon)
                .font(.title2)
                .foregroundStyle(.secondary)
            Text(message).font(.headline)
            Text(hint).font(.caption).foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 24)
    }

    // MARK: Reload

    private func reload() async {
        guard let api else {
            loading = false
            loadError = "Pair with a HUD to view Mills."
            return
        }
        loading = true
        loadError = nil
        async let runs = (try? await api.pipelineRuns()) ?? []
        async let snapshot = (try? await api.latestKPI(window: "1d"))
        let (loadedRuns, loadedKPI) = await (runs, snapshot)
        pipelineRuns = loadedRuns
        kpi = loadedKPI ?? nil
        loading = false
    }

    // MARK: Formatters

    private func fmtPercent(_ v: Double?) -> String? {
        guard let v else { return nil }
        return String(format: "%.0f%%", v * 100)
    }

    private func fmtUSD(_ v: Double?) -> String? {
        guard let v else { return nil }
        return String(format: "$%.2f", v)
    }

    private func fmtCount(_ v: Double?) -> String? {
        guard let v else { return nil }
        return String(format: "%.0f", v)
    }
}

#Preview {
    NavigationStack {
        MillsScreen(api: MillsScreenPreviewAPI())
    }
}

private struct MillsScreenPreviewAPI: MillsAPIProtocol, Sendable {
    func pipelineRuns() async throws -> [MillsPipelineRun] {
        [
            MillsPipelineRun(id: "PIPE-001", backlogID: "BACK-A", template: "mills-default",
                            state: "implementing", attempts: 1, depth: 0),
            MillsPipelineRun(id: "PIPE-001-S1", backlogID: "BACK-A-CHILD", template: "mills-default",
                            state: "testing", attempts: 1, parentRunID: "PIPE-001", depth: 1),
        ]
    }
    func latestKPI(window: String) async throws -> MillsKPISnapshot? {
        MillsKPISnapshot(metrics: [
            "pipeline_merge_rate": 0.93,
            "council_cost_per_day_usd": 4.22,
            "audit_findings_count": 7,
        ])
    }
}
