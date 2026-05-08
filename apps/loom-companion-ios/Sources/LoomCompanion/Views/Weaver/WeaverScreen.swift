// WeaverScreen — S7b of weaver-qwen3 plan.
//
// Read-only surface for the HUD's weaver + aimodels endpoints. Mirrors
// the HUD WeaverPanel.svelte: status header (with degraded banner),
// metrics, recent queries, role defaults. No mutations land from this
// screen; submission is intentionally deferred until the daemon-socket
// auth contract for mobile is settled.
//
// Spec: services/loom-core/.loom/111-product-spec-weaver-qwen3-
// integration-2026-05-08.md (IOS-002).

import LoomCompanionKit
import SwiftUI

public struct WeaverScreen: View {
    @State private var status: WeaverStatus?
    @State private var history: [WeaverHistoryEntry] = []
    @State private var metrics: WeaverMetrics = WeaverMetrics()
    @State private var roles: [AIModelRoleEntry] = []
    @State private var overridePath: String?
    @State private var loading = true
    @State private var loadError: String?

    private let api: WeaverAPIProtocol?

    public init(apiClient: APIClient?) {
        self.api = apiClient.map(WeaverAPI.init(client:))
    }

    /// Test-only initializer accepting a fake protocol implementation.
    public init(api: WeaverAPIProtocol?) {
        self.api = api
    }

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if let err = loadError {
                    errorCard(err)
                } else if loading && status == nil {
                    ProgressView("Loading weaver...")
                        .frame(maxWidth: .infinity, alignment: .center)
                        .padding()
                } else if let s = status {
                    if s.isDegraded {
                        degradedCard(s)
                    }
                    statusCard(s)
                    metricsCard(metrics)
                    if !roles.isEmpty {
                        rolesCard(roles, overridePath: overridePath)
                    }
                    historyCard(history)
                } else {
                    EmptyCard(text: "Weaver is not configured.")
                }
            }
            .padding(.vertical)
        }
        .navigationTitle("Weaver")
        .refreshable { await reload() }
        .task { await reload() }
    }

    // MARK: - Cards

    private func degradedCard(_ s: WeaverStatus) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                Text("Model preflight: degraded")
                    .font(.headline)
                    .foregroundStyle(.orange)
            }
            if let e = s.catalogError, !e.isEmpty {
                Text("FlexInfer /v1/models unreachable: \(e)")
                    .font(.subheadline)
            } else {
                let count = s.missingModels?.count ?? 0
                Text("\(count) configured model\(count == 1 ? "" : "s") not advertised by FlexInfer.")
                    .font(.subheadline)
            }
            if let missing = s.missingModels, !missing.isEmpty {
                modelChipRow(missing, tone: .missing)
            }
            if let ready = s.readyModels, !ready.isEmpty {
                Text("Ready (\(ready.count)):")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                modelChipRow(ready, tone: .ready)
            }
        }
        .padding()
        .background(Color.orange.opacity(0.10))
        .overlay(
            RoundedRectangle(cornerRadius: 12).stroke(Color.orange, lineWidth: 1)
        )
        .padding(.horizontal)
    }

    private func statusCard(_ s: WeaverStatus) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            row("Enabled", value: s.enabled ? "yes" : "no")
            if let r = s.routerModel { row("Router model", value: r) }
            if let sub = s.subagentModel { row("Subagent model", value: sub) }
            if let domains = s.domains {
                row("Domains", value: "\(domains.count)")
            }
        }
        .padding()
        .background(Color.gray.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    private func metricsCard(_ m: WeaverMetrics) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Metrics").font(.headline)
            row("Total queries", value: "\(m.totalQueries)")
            row("Avg latency", value: m.avgLatencyMs >= 1000
                ? String(format: "%.1fs", m.avgLatencyMs / 1000)
                : "\(Int(m.avgLatencyMs))ms")
            row("Error rate", value: String(format: "%.1f%%", m.errorRate * 100))
            row("Total tokens", value: "\(m.totalTokens)")
        }
        .padding()
        .background(Color.gray.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    private func rolesCard(_ rs: [AIModelRoleEntry], overridePath: String?) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Role defaults").font(.headline)
            ForEach(rs) { r in
                HStack(alignment: .firstTextBaseline) {
                    Text(r.role)
                        .font(.system(.subheadline, design: .monospaced))
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 8)
                    Text(r.primary.isEmpty ? "—" : r.primary)
                        .font(.system(.subheadline, design: .monospaced))
                }
                .padding(.vertical, 2)
            }
            if let path = overridePath, !path.isEmpty {
                Text("Override: \(path)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.top, 4)
            }
        }
        .padding()
        .background(Color.gray.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    private func historyCard(_ entries: [WeaverHistoryEntry]) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Recent queries (\(entries.count))").font(.headline)
            if entries.isEmpty {
                Text("No queries yet.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(entries.prefix(20)) { e in
                    historyRow(e)
                    Divider()
                }
            }
        }
        .padding()
        .background(Color.gray.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    private func historyRow(_ e: WeaverHistoryEntry) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack {
                Text(e.status ?? "?")
                    .font(.caption)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 1)
                    .background(badgeColor(for: e.status).opacity(0.18))
                    .foregroundStyle(badgeColor(for: e.status))
                    .clipShape(Capsule())
                Spacer()
                if let ms = e.latencyMs {
                    Text("\(ms)ms")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                if let t = e.totalTokens {
                    Text("\(t)t")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            Text(e.query ?? "(no query)")
                .font(.subheadline)
                .lineLimit(2)
            if let domains = e.domainsUsed, !domains.isEmpty {
                Text("domains: \(domains.joined(separator: ", "))")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            if let parent = e.parentSessionId, !parent.isEmpty {
                Text("parent session: \(parent)")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 4)
    }

    private func errorCard(_ msg: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Could not load weaver")
                .font(.headline)
                .foregroundStyle(.red)
            Text(msg)
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
        .padding()
        .background(Color.red.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .padding(.horizontal)
    }

    // MARK: - Helpers

    private func row(_ label: String, value: String) -> some View {
        HStack {
            Text(label).font(.subheadline).foregroundStyle(.secondary)
            Spacer()
            Text(value).font(.system(.subheadline, design: .monospaced))
        }
    }

    private enum ChipTone { case missing, ready }

    private func modelChipRow(_ models: [String], tone: ChipTone) -> some View {
        FlowLayout(spacing: 6) {
            ForEach(models, id: \.self) { m in
                Text(m)
                    .font(.caption2)
                    .monospaced()
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(tone == .missing ? Color.red.opacity(0.12) : Color.green.opacity(0.10))
                    .foregroundStyle(tone == .missing ? Color.red : Color.green)
                    .overlay(
                        RoundedRectangle(cornerRadius: 4).stroke(
                            tone == .missing ? Color.red : Color.green, lineWidth: 1
                        )
                    )
                    .clipShape(RoundedRectangle(cornerRadius: 4))
            }
        }
    }

    private func badgeColor(for status: String?) -> Color {
        switch status {
        case "ok", "success": return .green
        case "error": return .red
        default: return .secondary
        }
    }

    private func reload() async {
        guard let api else {
            loading = false
            return
        }
        loading = true
        loadError = nil
        async let s = api.status()
        async let h = api.history()
        async let m = api.metrics()
        async let r = api.roles()
        do {
            self.status = try await s
            self.history = (try await h).entries ?? []
            self.metrics = try await m
            let rolesResp = try await r
            self.roles = rolesResp.roles
            self.overridePath = rolesResp.overridePath
        } catch {
            self.loadError = "\(error)"
        }
        loading = false
    }
}

// MARK: - Tiny FlowLayout

/// Minimal flow layout for chip rows. Avoids pulling in additional
/// dependencies; iOS 17+ Layout API gives us this for free.
private struct FlowLayout: Layout {
    var spacing: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowHeight: CGFloat = 0
        var totalWidth: CGFloat = 0
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x + size.width > maxWidth {
                y += rowHeight + spacing
                x = 0
                rowHeight = 0
            }
            rowHeight = max(rowHeight, size.height)
            x += size.width + spacing
            totalWidth = max(totalWidth, x)
        }
        return CGSize(width: totalWidth, height: y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX
        var y = bounds.minY
        var rowHeight: CGFloat = 0
        for sub in subviews {
            let size = sub.sizeThatFits(.unspecified)
            if x + size.width > bounds.maxX {
                y += rowHeight + spacing
                x = bounds.minX
                rowHeight = 0
            }
            sub.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
    }
}

private struct EmptyCard: View {
    let text: String
    var body: some View {
        Text(text)
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .padding()
            .frame(maxWidth: .infinity)
            .background(Color.gray.opacity(0.08))
            .clipShape(RoundedRectangle(cornerRadius: 12))
            .padding(.horizontal)
    }
}

#Preview {
    NavigationStack {
        WeaverScreen(api: WeaverScreenPreviewAPI())
    }
}

private struct WeaverScreenPreviewAPI: WeaverAPIProtocol, Sendable {
    func status() async throws -> WeaverStatus {
        WeaverStatus(
            enabled: true,
            routerModel: "qwen3-1p7b-tools-radeonvii",
            subagentModel: "qwen3-8b",
            domains: [
                WeaverDomainSummary(name: "ops", description: "Operations", model: "qwen3-8b", backend: "flexinfer", tools: ["t1", "t2"]),
                WeaverDomainSummary(name: "ci", model: "qwen3-8b", backend: "flexinfer", tools: ["t1"]),
            ],
            degraded: false,
            missingModels: [],
            readyModels: ["qwen3-1p7b-tools-radeonvii", "qwen3-8b"],
            catalogSize: 8
        )
    }

    func history() async throws -> WeaverHistoryResponse {
        WeaverHistoryResponse(entries: [
            WeaverHistoryEntry(
                queryId: "q1",
                query: "What is the latest cluster status?",
                status: "ok",
                latencyMs: 1280,
                totalTokens: 412,
                domainsUsed: ["ops"]
            )
        ])
    }

    func metrics() async throws -> WeaverMetrics {
        WeaverMetrics(totalQueries: 12, avgLatencyMs: 1450, errorRate: 0.0, totalTokens: 4892, errorCount: 0)
    }

    func roles() async throws -> AIModelRolesResponse {
        AIModelRolesResponse(
            roles: [
                AIModelRoleEntry(role: "weaver-router", primary: "qwen3-1p7b-tools-radeonvii"),
                AIModelRoleEntry(role: "weaver-subagent", primary: "qwen3-8b"),
                AIModelRoleEntry(role: "mills-judge", primary: "qwen3-8b"),
            ],
            overridePath: "/Users/example/.config/loom/aimodel-roles.yaml"
        )
    }
}
