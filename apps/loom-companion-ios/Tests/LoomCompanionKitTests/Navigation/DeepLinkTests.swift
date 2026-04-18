import Testing
import Foundation
@testable import LoomCompanionKit

@Suite("DeepLink parse + build")
struct DeepLinkTests {

    // MARK: - Primary surfaces

    @Test("Primary surfaces parse correctly")
    func primarySurfaces() {
        #expect(DeepLink.from(URL(string: "loom://dashboard")!) == .dashboard)
        #expect(DeepLink.from(URL(string: "loom://people")!) == .people)
        #expect(DeepLink.from(URL(string: "loom://work")!) == .work)
        #expect(DeepLink.from(URL(string: "loom://alerts")!) == .alerts)
        #expect(DeepLink.from(URL(string: "loom://connection")!) == .connection)
        #expect(DeepLink.from(URL(string: "loom://handoff")!) == .handoff)
        // Pluralized alias
        #expect(DeepLink.from(URL(string: "loom://handoffs")!) == .handoff)
    }

    @Test("Case-insensitive host parsing")
    func caseInsensitiveHost() {
        #expect(DeepLink.from(URL(string: "loom://Dashboard")!) == .dashboard)
        #expect(DeepLink.from(URL(string: "loom://SESSION/abc")!) == .session(id: "abc"))
    }

    // MARK: - Single-object routes

    @Test("Session detail parses")
    func sessionDetail() {
        let link = DeepLink.from(URL(string: "loom://session/svc-abc123")!)
        #expect(link == .session(id: "svc-abc123"))
    }

    @Test("Agent detail parses")
    func agentDetail() {
        let link = DeepLink.from(URL(string: "loom://agent/claude-code")!)
        #expect(link == .agent(id: "claude-code"))
    }

    @Test("Spawn detail parses")
    func spawnDetail() {
        #expect(DeepLink.from(URL(string: "loom://spawn/spawn-42")!) == .spawn(id: "spawn-42"))
    }

    @Test("Alert detail parses")
    func alertDetail() {
        #expect(DeepLink.from(URL(string: "loom://alert/alert-9")!) == .alert(id: "alert-9"))
    }

    @Test("Workflow detail parses (view)")
    func workflowView() {
        #expect(DeepLink.from(URL(string: "loom://workflow/wf-1")!) == .workflow(id: "wf-1", approve: false))
    }

    @Test("Workflow approve intent parses")
    func workflowApprove() {
        #expect(DeepLink.from(URL(string: "loom://workflow/wf-1/approve")!) == .workflow(id: "wf-1", approve: true))
    }

    // MARK: - Filtered list routes

    @Test("Sessions filter parses both params")
    func sessionsFilter() {
        let link = DeepLink.from(URL(string: "loom://sessions?status=active&agent=claude-code")!)
        #expect(link == .sessions(status: "active", agentId: "claude-code"))
    }

    @Test("Sessions filter with no params")
    func sessionsFilterEmpty() {
        #expect(DeepLink.from(URL(string: "loom://sessions")!) == .sessions(status: nil, agentId: nil))
    }

    @Test("Agents filter parses")
    func agentsFilter() {
        let link = DeepLink.from(URL(string: "loom://agents?status=idle&type=gemini")!)
        #expect(link == .agents(status: "idle", type: "gemini"))
    }

    @Test("Tasks filter with all params")
    func tasksFilterFull() {
        let link = DeepLink.from(URL(string: "loom://tasks?status=blocked&agent=codex&session=svc-1")!)
        #expect(link == .tasks(status: "blocked", agentId: "codex", sessionId: "svc-1"))
    }

    @Test("Empty query values treated as nil")
    func emptyQueryValues() {
        let link = DeepLink.from(URL(string: "loom://tasks?status=&agent=&session=")!)
        #expect(link == .tasks(status: nil, agentId: nil, sessionId: nil))
    }

    // MARK: - Invalid input

    @Test("Unknown scheme returns nil")
    func unknownScheme() {
        #expect(DeepLink.from(URL(string: "https://loom/dashboard")!) == nil)
    }

    @Test("Unknown host returns nil")
    func unknownHost() {
        #expect(DeepLink.from(URL(string: "loom://wakawaka")!) == nil)
    }

    @Test("Session without ID returns nil")
    func sessionWithoutId() {
        #expect(DeepLink.from(URL(string: "loom://session")!) == nil)
        #expect(DeepLink.from(URL(string: "loom://session/")!) == nil)
    }

    @Test("Agent without ID returns nil")
    func agentWithoutId() {
        #expect(DeepLink.from(URL(string: "loom://agent")!) == nil)
    }

    // MARK: - Build (urlString)

    @Test("Primary surfaces build to canonical strings")
    func buildsPrimary() {
        #expect(DeepLink.dashboard.urlString == "loom://dashboard")
        #expect(DeepLink.people.urlString == "loom://people")
        #expect(DeepLink.alerts.urlString == "loom://alerts")
        #expect(DeepLink.handoff.urlString == "loom://handoff")
    }

    @Test("Single-object routes build correctly")
    func buildsSingleObject() {
        #expect(DeepLink.session(id: "abc").urlString == "loom://session/abc")
        #expect(DeepLink.agent(id: "claude-code").urlString == "loom://agent/claude-code")
        #expect(DeepLink.spawn(id: "s1").urlString == "loom://spawn/s1")
        #expect(DeepLink.alert(id: "a1").urlString == "loom://alert/a1")
    }

    @Test("Workflow build switches on approve flag")
    func buildsWorkflow() {
        #expect(DeepLink.workflow(id: "wf-1", approve: false).urlString == "loom://workflow/wf-1")
        #expect(DeepLink.workflow(id: "wf-1", approve: true).urlString == "loom://workflow/wf-1/approve")
    }

    @Test("Filtered list omits nil params")
    func buildsFilterOmitsNil() {
        #expect(DeepLink.sessions(status: nil, agentId: nil).urlString == "loom://sessions")
        #expect(
            DeepLink.sessions(status: "active", agentId: nil).urlString == "loom://sessions?status=active"
        )
    }

    @Test("Filtered list includes all non-nil params")
    func buildsFilterFull() {
        let url = DeepLink.tasks(status: "blocked", agentId: "claude", sessionId: "s-1").urlString
        // Order must be stable so shared links are canonical
        #expect(url == "loom://tasks?status=blocked&agent=claude&session=s-1")
    }

    // MARK: - Round-trip (parse ∘ build == identity)

    @Test("Round-trip preserves all primary surfaces")
    func roundTripPrimary() {
        let cases: [DeepLink] = [.dashboard, .people, .work, .alerts, .connection, .handoff]
        for link in cases {
            let rt = DeepLink.from(URL(string: link.urlString)!)
            #expect(rt == link, "round-trip failed for \(link)")
        }
    }

    @Test("Round-trip preserves single-object routes")
    func roundTripSingleObject() {
        let cases: [DeepLink] = [
            .session(id: "svc-abc"),
            .agent(id: "claude-code"),
            .spawn(id: "spawn-42"),
            .alert(id: "alert-9"),
            .workflow(id: "wf-1", approve: false),
            .workflow(id: "wf-2", approve: true),
        ]
        for link in cases {
            let rt = DeepLink.from(URL(string: link.urlString)!)
            #expect(rt == link, "round-trip failed for \(link)")
        }
    }

    @Test("Round-trip preserves filter routes")
    func roundTripFilters() {
        let cases: [DeepLink] = [
            .sessions(status: nil, agentId: nil),
            .sessions(status: "active", agentId: nil),
            .sessions(status: "ended", agentId: "claude"),
            .agents(status: "idle", type: "gemini"),
            .agents(status: nil, type: "codex"),
            .tasks(status: "blocked", agentId: "claude", sessionId: "s-1"),
            .tasks(status: nil, agentId: nil, sessionId: "s-2"),
        ]
        for link in cases {
            let rt = DeepLink.from(URL(string: link.urlString)!)
            #expect(rt == link, "round-trip failed for \(link)")
        }
    }

    // MARK: - Routing

    @Test("DestinationGroup routes correctly")
    func destinationGroups() {
        #expect(DeepLink.dashboard.destinationGroup == .dashboard)
        #expect(DeepLink.session(id: "x").destinationGroup == .people)
        #expect(DeepLink.agents(status: nil, type: nil).destinationGroup == .people)
        #expect(DeepLink.tasks(status: nil, agentId: nil, sessionId: nil).destinationGroup == .work)
        #expect(DeepLink.workflow(id: "x", approve: false).destinationGroup == .work)
        #expect(DeepLink.spawn(id: "x").destinationGroup == .work)
        #expect(DeepLink.handoff.destinationGroup == .work)
        #expect(DeepLink.alert(id: "x").destinationGroup == .alerts)
        #expect(DeepLink.connection.destinationGroup == .connection)
    }

    // MARK: - Share metadata

    // MARK: - Configure (one-shot bootstrap via `make mobile-app-run-device`)

    @Test("Configure with gateway + CF service token round-trips")
    func configureGatewayRoundTrip() {
        let spec = DeepLink.ConfigureSpec(
            mode: "gateway",
            url: "https://hud.flexinfer.ai",
            bearer: "t0k3n-abc",
            cfClientID: "cf-id",
            cfClientSecret: "cf-secret"
        )
        let link = DeepLink.configure(spec)
        let rt = DeepLink.from(URL(string: link.urlString)!)
        #expect(rt == link)
    }

    @Test("Configure without CF fields round-trips")
    func configureLANRoundTrip() {
        let spec = DeepLink.ConfigureSpec(
            mode: "lan",
            url: "http://127.0.0.1:3333",
            bearer: "dev-token"
        )
        let link = DeepLink.configure(spec)
        let rt = DeepLink.from(URL(string: link.urlString)!)
        #expect(rt == link)
    }

    @Test("Configure defaults mode to gateway when omitted")
    func configureDefaultsMode() {
        let url = URL(string: "loom://configure?url=https%3A%2F%2Fhud.example&bearer=abc")!
        guard case let .configure(spec) = DeepLink.from(url) else {
            #expect(Bool(false), "expected .configure case")
            return
        }
        #expect(spec.mode == "gateway")
        #expect(spec.url == "https://hud.example")
        #expect(spec.bearer == "abc")
        #expect(spec.cfClientID == nil)
    }

    @Test("Configure missing required params returns nil")
    func configureMissingRequiredParams() {
        // Missing bearer
        #expect(DeepLink.from(URL(string: "loom://configure?url=https%3A%2F%2Fhud.example")!) == nil)
        // Missing url
        #expect(DeepLink.from(URL(string: "loom://configure?bearer=abc")!) == nil)
    }

    @Test("Configure routes to connection tab group")
    func configureRoutesToConnection() {
        let link = DeepLink.configure(.init(mode: "gateway", url: "https://x", bearer: "t"))
        #expect(link.destinationGroup == .connection)
    }

    @Test("shareTitle describes all cases")
    func shareTitles() {
        #expect(DeepLink.dashboard.shareTitle == "Loom Dashboard")
        #expect(DeepLink.session(id: "svc-1").shareTitle.contains("svc-1"))
        #expect(DeepLink.agent(id: "claude").shareTitle.contains("claude"))
        #expect(DeepLink.sessions(status: "active", agentId: nil).shareTitle == "Sessions (active)")
        #expect(DeepLink.sessions(status: nil, agentId: nil).shareTitle == "Sessions")
        #expect(DeepLink.workflow(id: "wf-1", approve: true).shareTitle == "Approve workflow wf-1")
    }
}
