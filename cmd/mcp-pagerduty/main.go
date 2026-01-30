// mcp-pagerduty provides MCP tools for PagerDuty incident management.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version = "0.1.0"

	pdAPIKey  = os.Getenv("PAGERDUTY_API_KEY")
	pdBaseURL = getEnv("PAGERDUTY_BASE_URL", "https://api.pagerduty.com")

	httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-pagerduty", version)
	server.SetInstructions("PagerDuty incident management tools. Configure with PAGERDUTY_API_KEY.")

	// Incidents
	server.AddTool(mcp.Tool{
		Name:        "pd_list_incidents",
		Description: "List incidents with optional filters",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status: triggered, acknowledged, resolved",
				},
				"urgency": map[string]any{
					"type":        "string",
					"description": "Filter by urgency: high, low",
				},
				"service_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by service IDs",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Start of date range (ISO8601)",
				},
				"until": map[string]any{
					"type":        "string",
					"description": "End of date range (ISO8601)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of incidents to return (default: 25)",
				},
			},
		},
	}, handleListIncidents)

	server.AddTool(mcp.Tool{
		Name:        "pd_get_incident",
		Description: "Get details of a specific incident",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Incident ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleGetIncident)

	server.AddTool(mcp.Tool{
		Name:        "pd_list_incident_alerts",
		Description: "List alerts for an incident",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Incident ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleListIncidentAlerts)

	server.AddTool(mcp.Tool{
		Name:        "pd_list_incident_notes",
		Description: "List notes for an incident",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Incident ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleListIncidentNotes)

	server.AddTool(mcp.Tool{
		Name:        "pd_list_incident_log_entries",
		Description: "List log entries (timeline) for an incident",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Incident ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleListIncidentLogEntries)

	// Services
	server.AddTool(mcp.Tool{
		Name:        "pd_list_services",
		Description: "List all services",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Filter services by name",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of services to return",
				},
			},
		},
	}, handleListServices)

	server.AddTool(mcp.Tool{
		Name:        "pd_get_service",
		Description: "Get details of a specific service",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Service ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleGetService)

	// On-Calls
	server.AddTool(mcp.Tool{
		Name:        "pd_list_oncalls",
		Description: "List current on-call entries",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"schedule_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by schedule IDs",
				},
				"escalation_policy_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Filter by escalation policy IDs",
				},
			},
		},
	}, handleListOnCalls)

	// Schedules
	server.AddTool(mcp.Tool{
		Name:        "pd_list_schedules",
		Description: "List all schedules",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Filter schedules by name",
				},
			},
		},
	}, handleListSchedules)

	server.AddTool(mcp.Tool{
		Name:        "pd_get_schedule",
		Description: "Get details of a specific schedule with current on-call",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Schedule ID",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Start of date range (ISO8601)",
				},
				"until": map[string]any{
					"type":        "string",
					"description": "End of date range (ISO8601)",
				},
			},
			Required: []string{"id"},
		},
	}, handleGetSchedule)

	// Escalation Policies
	server.AddTool(mcp.Tool{
		Name:        "pd_list_escalation_policies",
		Description: "List all escalation policies",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Filter by name",
				},
			},
		},
	}, handleListEscalationPolicies)

	// Users
	server.AddTool(mcp.Tool{
		Name:        "pd_list_users",
		Description: "List all users",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Filter users by name or email",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of users to return",
				},
			},
		},
	}, handleListUsers)

	server.AddTool(mcp.Tool{
		Name:        "pd_get_user",
		Description: "Get details of a specific user",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "User ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleGetUser)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// pdRequest makes an authenticated request to PagerDuty API
func pdRequest(ctx context.Context, method, path string, query url.Values) (map[string]any, error) {
	apiURL := pdBaseURL + path
	if len(query) > 0 {
		apiURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Token token="+pdAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]any
		if json.Unmarshal(body, &errResp) == nil {
			if errObj, ok := errResp["error"].(map[string]any); ok {
				return nil, fmt.Errorf("PagerDuty error (%d): %v", resp.StatusCode, errObj["message"])
			}
		}
		return nil, fmt.Errorf("PagerDuty error (%d): %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
	}

	return result, nil
}

func handleListIncidents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := url.Values{}

	if status, ok := args["status"].(string); ok && status != "" {
		query.Add("statuses[]", status)
	}
	if urgency, ok := args["urgency"].(string); ok && urgency != "" {
		query.Set("urgencies[]", urgency)
	}
	if serviceIDs, ok := args["service_ids"].([]any); ok {
		for _, id := range serviceIDs {
			if s, ok := id.(string); ok {
				query.Add("service_ids[]", s)
			}
		}
	}
	if since, ok := args["since"].(string); ok && since != "" {
		query.Set("since", since)
	}
	if until, ok := args["until"].(string); ok && until != "" {
		query.Set("until", until)
	}
	if limit, ok := args["limit"].(float64); ok {
		query.Set("limit", strconv.Itoa(int(limit)))
	} else {
		query.Set("limit", "25")
	}

	result, err := pdRequest(ctx, "GET", "/incidents", query)
	if err != nil {
		return nil, err
	}

	// Format incidents for readability
	incidents := []map[string]any{}
	if incidentList, ok := result["incidents"].([]any); ok {
		for _, inc := range incidentList {
			if incident, ok := inc.(map[string]any); ok {
				formatted := map[string]any{
					"id":              incident["id"],
					"incident_number": incident["incident_number"],
					"title":           incident["title"],
					"status":          incident["status"],
					"urgency":         incident["urgency"],
					"created_at":      incident["created_at"],
					"html_url":        incident["html_url"],
				}
				if service, ok := incident["service"].(map[string]any); ok {
					formatted["service"] = service["summary"]
				}
				if assignees, ok := incident["assignments"].([]any); ok && len(assignees) > 0 {
					names := []string{}
					for _, a := range assignees {
						if assignment, ok := a.(map[string]any); ok {
							if assignee, ok := assignment["assignee"].(map[string]any); ok {
								if name, ok := assignee["summary"].(string); ok {
									names = append(names, name)
								}
							}
						}
					}
					formatted["assignees"] = names
				}
				incidents = append(incidents, formatted)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"incidents": incidents,
		"count":     len(incidents),
	})
}

func handleGetIncident(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	result, err := pdRequest(ctx, "GET", "/incidents/"+id, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result["incident"])
}

func handleListIncidentAlerts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	result, err := pdRequest(ctx, "GET", "/incidents/"+id+"/alerts", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"alerts": result["alerts"],
	})
}

func handleListIncidentNotes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	result, err := pdRequest(ctx, "GET", "/incidents/"+id+"/notes", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"notes": result["notes"],
	})
}

func handleListIncidentLogEntries(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	result, err := pdRequest(ctx, "GET", "/incidents/"+id+"/log_entries", nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"log_entries": result["log_entries"],
	})
}

func handleListServices(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := url.Values{}

	if q, ok := args["query"].(string); ok && q != "" {
		query.Set("query", q)
	}
	if limit, ok := args["limit"].(float64); ok {
		query.Set("limit", strconv.Itoa(int(limit)))
	}

	result, err := pdRequest(ctx, "GET", "/services", query)
	if err != nil {
		return nil, err
	}

	// Format services
	services := []map[string]any{}
	if serviceList, ok := result["services"].([]any); ok {
		for _, svc := range serviceList {
			if service, ok := svc.(map[string]any); ok {
				formatted := map[string]any{
					"id":          service["id"],
					"name":        service["name"],
					"description": service["description"],
					"status":      service["status"],
					"html_url":    service["html_url"],
				}
				if ep, ok := service["escalation_policy"].(map[string]any); ok {
					formatted["escalation_policy"] = ep["summary"]
				}
				services = append(services, formatted)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"services": services,
		"count":    len(services),
	})
}

func handleGetService(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	result, err := pdRequest(ctx, "GET", "/services/"+id, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result["service"])
}

func handleListOnCalls(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := url.Values{}

	if scheduleIDs, ok := args["schedule_ids"].([]any); ok {
		for _, id := range scheduleIDs {
			if s, ok := id.(string); ok {
				query.Add("schedule_ids[]", s)
			}
		}
	}
	if epIDs, ok := args["escalation_policy_ids"].([]any); ok {
		for _, id := range epIDs {
			if s, ok := id.(string); ok {
				query.Add("escalation_policy_ids[]", s)
			}
		}
	}

	result, err := pdRequest(ctx, "GET", "/oncalls", query)
	if err != nil {
		return nil, err
	}

	// Format on-calls
	oncalls := []map[string]any{}
	if oncallList, ok := result["oncalls"].([]any); ok {
		for _, oc := range oncallList {
			if oncall, ok := oc.(map[string]any); ok {
				formatted := map[string]any{
					"escalation_level": oncall["escalation_level"],
					"start":            oncall["start"],
					"end":              oncall["end"],
				}
				if user, ok := oncall["user"].(map[string]any); ok {
					formatted["user"] = map[string]any{
						"id":    user["id"],
						"name":  user["summary"],
						"email": user["email"],
					}
				}
				if schedule, ok := oncall["schedule"].(map[string]any); ok {
					formatted["schedule"] = schedule["summary"]
				}
				if ep, ok := oncall["escalation_policy"].(map[string]any); ok {
					formatted["escalation_policy"] = ep["summary"]
				}
				oncalls = append(oncalls, formatted)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"oncalls": oncalls,
		"count":   len(oncalls),
	})
}

func handleListSchedules(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := url.Values{}

	if q, ok := args["query"].(string); ok && q != "" {
		query.Set("query", q)
	}

	result, err := pdRequest(ctx, "GET", "/schedules", query)
	if err != nil {
		return nil, err
	}

	// Format schedules
	schedules := []map[string]any{}
	if scheduleList, ok := result["schedules"].([]any); ok {
		for _, sch := range scheduleList {
			if schedule, ok := sch.(map[string]any); ok {
				formatted := map[string]any{
					"id":          schedule["id"],
					"name":        schedule["name"],
					"description": schedule["description"],
					"time_zone":   schedule["time_zone"],
					"html_url":    schedule["html_url"],
				}
				schedules = append(schedules, formatted)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"schedules": schedules,
		"count":     len(schedules),
	})
}

func handleGetSchedule(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	query := url.Values{}
	if since, ok := args["since"].(string); ok && since != "" {
		query.Set("since", since)
	}
	if until, ok := args["until"].(string); ok && until != "" {
		query.Set("until", until)
	}

	result, err := pdRequest(ctx, "GET", "/schedules/"+id, query)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result["schedule"])
}

func handleListEscalationPolicies(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := url.Values{}

	if q, ok := args["query"].(string); ok && q != "" {
		query.Set("query", q)
	}

	result, err := pdRequest(ctx, "GET", "/escalation_policies", query)
	if err != nil {
		return nil, err
	}

	// Format policies
	policies := []map[string]any{}
	if policyList, ok := result["escalation_policies"].([]any); ok {
		for _, pol := range policyList {
			if policy, ok := pol.(map[string]any); ok {
				formatted := map[string]any{
					"id":          policy["id"],
					"name":        policy["name"],
					"description": policy["description"],
					"num_loops":   policy["num_loops"],
					"html_url":    policy["html_url"],
				}
				if rules, ok := policy["escalation_rules"].([]any); ok {
					formatted["num_rules"] = len(rules)
				}
				policies = append(policies, formatted)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"escalation_policies": policies,
		"count":               len(policies),
	})
}

func handleListUsers(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := url.Values{}

	if q, ok := args["query"].(string); ok && q != "" {
		query.Set("query", q)
	}
	if limit, ok := args["limit"].(float64); ok {
		query.Set("limit", strconv.Itoa(int(limit)))
	}

	result, err := pdRequest(ctx, "GET", "/users", query)
	if err != nil {
		return nil, err
	}

	// Format users
	users := []map[string]any{}
	if userList, ok := result["users"].([]any); ok {
		for _, usr := range userList {
			if user, ok := usr.(map[string]any); ok {
				formatted := map[string]any{
					"id":       user["id"],
					"name":     user["name"],
					"email":    user["email"],
					"role":     user["role"],
					"html_url": user["html_url"],
				}
				users = append(users, formatted)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"users": users,
		"count": len(users),
	})
}

func handleGetUser(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	result, err := pdRequest(ctx, "GET", "/users/"+id, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result["user"])
}
