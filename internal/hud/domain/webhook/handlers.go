package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/crb2nu/loom/internal/spawn"
)

// handleGitLabWebhook handles POST /api/webhook/gitlab.
//
// Authentication is the X-Gitlab-Token shared secret only — GitLab
// cannot send the HUD admin token, and the secret-comparison via
// verifyGitLabToken provides the same blast-radius guarantee. The
// /api/webhook/{config,events} inspection endpoints retain the admin-
// token gate via their own RequireAdminToken calls.
func (d *Domain) handleGitLabWebhook(w http.ResponseWriter, r *http.Request) {
	cfg := d.deps.WebhookConfig()
	if !cfg.InboundEnabled {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "inbound webhooks not enabled", nil)
		return
	}

	// Verify GitLab token. When no secret is configured the verifier
	// returns true (open by default for local dev), so production
	// deployments must always set WEBHOOK_GITLAB_SECRET.
	token := r.Header.Get("X-Gitlab-Token")
	if !verifyGitLabToken(token, cfg.GitLabSecret) {
		d.deps.WriteError(w, http.StatusUnauthorized, "invalid gitlab token", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "failed to read body", err)
		return
	}

	var ev GitLabPipelineEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid JSON payload", err)
		return
	}

	logEntry := WebhookEvent{
		Source:    "gitlab",
		EventType: ev.ObjectKind,
		Project:   ev.Project.PathWithNamespace,
		Ref:       ev.ObjectAttributes.Ref,
		Status:    ev.ObjectAttributes.Status,
	}

	// Attempt to map event to spawn request. The mapper only fires
	// for failed pipelines today, so spawnReq != nil implies a CI
	// failure we want to act on.
	spawnReq := mapGitLabEvent(&ev)
	if spawnReq != nil {
		// Prefer routing the failure to a session that's actively
		// working the affected branch (the original author, in
		// almost every case) — spawning a fresh agent is wasteful
		// when someone is already at the keyboard. Falls back to
		// spawn-fresh below when no active session matches.
		branch := gitLabFailureBranch(&ev)
		matched := matchActiveAgentsForBranch(d.deps.ActiveAgentsForBranch(branch), branch)
		if len(matched) > 0 {
			task := gitLabFailureTask(&ev, branch)
			payload := map[string]any{
				"branch":      branch,
				"project":     ev.Project.PathWithNamespace,
				"pipeline_id": ev.ObjectAttributes.ID,
				"status":      ev.ObjectAttributes.Status,
				"task":        task,
				"agents":      activeAgentIDs(matched),
				"source":      "gitlab",
			}
			if ev.MergeRequest != nil {
				payload["mr_iid"] = ev.MergeRequest.IID
			}
			d.deps.BroadcastAgentEvent("ci.pipeline.failure.routed", payload)
			logEntry.Action = "routed"
		} else {
			spawner := d.deps.Spawner()
			if spawner != nil {
				spawnID, err := spawner.Spawn(r.Context(), *spawnReq)
				if err != nil {
					logEntry.Error = err.Error()
					d.deps.Logger().Warn("webhook spawn failed", "source", "gitlab", "error", err)
				} else {
					logEntry.SpawnID = spawnID
					logEntry.Action = "spawned"
				}
			} else {
				logEntry.Action = "spawn_unavailable"
			}
		}
	} else {
		logEntry.Action = "ignored"
	}

	d.eventLog.add(logEntry)
	d.deps.BroadcastAgentEvent("webhook.received", map[string]any{
		"source":  "gitlab",
		"project": ev.Project.PathWithNamespace,
		"ref":     ev.ObjectAttributes.Ref,
		"status":  ev.ObjectAttributes.Status,
		"action":  logEntry.Action,
	})

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"received": true,
		"action":   logEntry.Action,
		"spawn_id": logEntry.SpawnID,
	})
}

// handleGitHubWebhook handles POST /api/webhook/github.
//
// Authentication is the X-Hub-Signature-256 HMAC over the request body
// using the configured WEBHOOK_GITHUB_SECRET. Same rationale as
// handleGitLabWebhook for skipping the HUD admin-token gate — the
// originating system can't send that token, the HMAC is the
// canonical webhook auth mechanism.
func (d *Domain) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	cfg := d.deps.WebhookConfig()
	if !cfg.InboundEnabled {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "inbound webhooks not enabled", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "failed to read body", err)
		return
	}

	// Verify GitHub HMAC signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if !verifyGitHubSignature(signature, cfg.GitHubSecret, body) {
		d.deps.WriteError(w, http.StatusUnauthorized, "invalid github signature", nil)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")

	logEntry := WebhookEvent{
		Source:    "github",
		EventType: eventType,
	}

	switch eventType {
	case "check_suite":
		var ev GitHubCheckSuiteEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			d.deps.WriteError(w, http.StatusBadRequest, "invalid JSON payload", err)
			return
		}
		logEntry.Project = ev.Repository.FullName
		logEntry.Ref = ev.CheckSuite.HeadBranch
		logEntry.Status = ev.CheckSuite.Conclusion

		spawnReq := mapGitHubCheckSuiteEvent(&ev)
		if spawnReq != nil {
			d.trySpawn(r, spawnReq, &logEntry)
		} else {
			logEntry.Action = "ignored"
		}

	case "pull_request":
		var ev GitHubPullRequestEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			d.deps.WriteError(w, http.StatusBadRequest, "invalid JSON payload", err)
			return
		}
		logEntry.Project = ev.Repository.FullName
		logEntry.Ref = ev.PullRequest.Head.Ref
		logEntry.Status = ev.Action
		logEntry.Action = "ignored"
		logEntry.RawSummary = fmt.Sprintf("PR #%d: %s", ev.PullRequest.Number, ev.PullRequest.Title)

	default:
		logEntry.Action = "unsupported_event"
	}

	d.eventLog.add(logEntry)
	d.deps.BroadcastAgentEvent("webhook.received", map[string]any{
		"source":     "github",
		"event_type": eventType,
		"project":    logEntry.Project,
		"ref":        logEntry.Ref,
		"status":     logEntry.Status,
		"action":     logEntry.Action,
	})

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"received": true,
		"action":   logEntry.Action,
		"spawn_id": logEntry.SpawnID,
	})
}

// trySpawn attempts to spawn an agent from a webhook event.
func (d *Domain) trySpawn(r *http.Request, req *spawn.Request, logEntry *WebhookEvent) {
	// Prefer routing to an active session on the same branch before
	// spawning a fresh agent — see handleGitLabWebhook for the
	// equivalent path. Avoids waking a fresh devbox / k8s pod when
	// the original author is already at the keyboard on this branch.
	branch := req.BaseBranch
	matched := matchActiveAgentsForBranch(d.deps.ActiveAgentsForBranch(branch), branch)
	if len(matched) > 0 {
		d.deps.BroadcastAgentEvent("ci.pipeline.failure.routed", map[string]any{
			"branch":  branch,
			"project": req.Project,
			"task":    req.TaskDescription,
			"agents":  activeAgentIDs(matched),
			"source":  logEntry.Source,
		})
		logEntry.Action = "routed"
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		logEntry.Action = "spawn_unavailable"
		return
	}
	spawnID, err := spawner.Spawn(r.Context(), *req)
	if err != nil {
		logEntry.Error = err.Error()
		logEntry.Action = "spawn_failed"
		d.deps.Logger().Warn("webhook spawn failed", "source", logEntry.Source, "error", err)
	} else {
		logEntry.SpawnID = spawnID
		logEntry.Action = "spawned"
	}
}

// handleWebhookConfig handles GET /api/webhook/config.
func (d *Domain) handleWebhookConfig(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	cfg := d.deps.WebhookConfig()
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"inbound_enabled":    cfg.InboundEnabled,
		"gitlab_secret_set":  cfg.GitLabSecret != "",
		"github_secret_set":  cfg.GitHubSecret != "",
		"event_buffer_count": len(d.eventLog.all()),
	})
}

// handleWebhookEventLog handles GET /api/webhook/events.
func (d *Domain) handleWebhookEventLog(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	events := d.eventLog.all()
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
	})
}
