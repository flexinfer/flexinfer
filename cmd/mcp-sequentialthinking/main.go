// mcp-sequentialthinking provides a sequential thinking/reasoning MCP server
// for structured thought chains and step-by-step problem solving.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "dev"

// ThoughtStep represents a single step in a thought chain
type ThoughtStep struct {
	ID          int       `json:"id"`
	Thought     string    `json:"thought"`
	ThoughtType string    `json:"thought_type,omitempty"` // observation, hypothesis, conclusion, question, etc.
	Confidence  float64   `json:"confidence,omitempty"`   // 0.0 to 1.0
	Timestamp   time.Time `json:"timestamp"`
	ParentID    *int      `json:"parent_id,omitempty"` // for branching thoughts
}

// ThoughtChain represents a sequential chain of thoughts
type ThoughtChain struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description,omitempty"`
	Steps       []ThoughtStep `json:"steps"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Status      string        `json:"status"` // active, completed, abandoned
}

// ThinkingState holds all thought chains
type ThinkingState struct {
	Chains      map[string]*ThoughtChain `json:"chains"`
	ActiveChain string                   `json:"active_chain"`
	mu          sync.RWMutex
	persistPath string
	nextStepID  int
}

var state *ThinkingState

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	// Initialize state
	persistPath := os.Getenv("THINKING_PERSIST_PATH")
	if persistPath == "" {
		homeDir, _ := os.UserHomeDir()
		persistPath = homeDir + "/.local/share/mcp-sequentialthinking/state.json"
	}

	state = &ThinkingState{
		Chains:      make(map[string]*ThoughtChain),
		persistPath: persistPath,
		nextStepID:  1,
	}

	// Load existing state
	if err := state.load(); err != nil {
		logger.Warn("could not load state", "error", err)
	}

	logger.Info("starting server", "name", "mcp-sequentialthinking", "version", version, "path", persistPath)

	server := mcp.NewServer("mcp-sequentialthinking", version)
	server.SetInstructions("Sequential thinking server for structured reasoning. Use start_thinking to begin, add_thought to continue, and complete_chain to finish.")

	// start_thinking - Start a new thought chain
	server.AddTool(mcp.Tool{
		Name:        "start_thinking",
		Description: "Start a new thought chain for structured reasoning",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Title for the thought chain",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Description of the problem being solved",
				},
				"initial_thought": map[string]any{
					"type":        "string",
					"description": "Initial thought to start the chain",
				},
				"thought_type": map[string]any{
					"type":        "string",
					"description": "Type of thought: observation, hypothesis, question, etc.",
				},
			},
		},
	}, handleStartThinking)

	// add_thought - Add a thought to the chain
	server.AddTool(mcp.Tool{
		Name:        "add_thought",
		Description: "Add a thought step to the current or specified chain",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"thought": map[string]any{
					"type":        "string",
					"description": "The thought content",
				},
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID (uses active chain if not specified)",
				},
				"thought_type": map[string]any{
					"type":        "string",
					"description": "Type: observation, hypothesis, reasoning, conclusion, question",
				},
				"confidence": map[string]any{
					"type":        "number",
					"description": "Confidence level 0.0 to 1.0",
				},
			},
			Required: []string{"thought"},
		},
	}, handleAddThought)

	// get_chain - Get a thought chain
	server.AddTool(mcp.Tool{
		Name:        "get_chain",
		Description: "Get the current state of a thought chain",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID (uses active chain if not specified)",
				},
			},
		},
	}, handleGetChain)

	// list_chains - List all chains
	server.AddTool(mcp.Tool{
		Name:        "list_chains",
		Description: "List all thought chains",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status: active, completed, abandoned",
				},
			},
		},
	}, handleListChains)

	// set_active_chain - Set active chain
	server.AddTool(mcp.Tool{
		Name:        "set_active_chain",
		Description: "Set the active thought chain",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID to make active",
				},
			},
			Required: []string{"chain_id"},
		},
	}, handleSetActiveChain)

	// complete_chain - Complete a chain
	server.AddTool(mcp.Tool{
		Name:        "complete_chain",
		Description: "Mark a thought chain as completed with a conclusion",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID (uses active chain if not specified)",
				},
				"conclusion": map[string]any{
					"type":        "string",
					"description": "Final conclusion of the thought process",
				},
			},
		},
	}, handleCompleteChain)

	// branch_thought - Create a branch
	server.AddTool(mcp.Tool{
		Name:        "branch_thought",
		Description: "Create a branching thought from an existing step",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"parent_step_id": map[string]any{
					"type":        "integer",
					"description": "ID of the step to branch from",
				},
				"thought": map[string]any{
					"type":        "string",
					"description": "The alternative thought content",
				},
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID (uses active chain if not specified)",
				},
				"thought_type": map[string]any{
					"type":        "string",
					"description": "Type of thought",
				},
			},
			Required: []string{"parent_step_id", "thought"},
		},
	}, handleBranchThought)

	// delete_chain - Delete a chain
	server.AddTool(mcp.Tool{
		Name:        "delete_chain",
		Description: "Delete a thought chain",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID to delete",
				},
			},
			Required: []string{"chain_id"},
		},
	}, handleDeleteChain)

	// summarize_chain - Get summary
	server.AddTool(mcp.Tool{
		Name:        "summarize_chain",
		Description: "Get a summary of the thought chain's progression",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chain_id": map[string]any{
					"type":        "string",
					"description": "Chain ID (uses active chain if not specified)",
				},
			},
		},
	}, handleSummarizeChain)

	return server.Run(ctx)
}

func (s *ThinkingState) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var loaded struct {
		Chains      map[string]*ThoughtChain `json:"chains"`
		ActiveChain string                   `json:"active_chain"`
		NextStepID  int                      `json:"next_step_id"`
	}

	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	s.Chains = loaded.Chains
	s.ActiveChain = loaded.ActiveChain
	s.nextStepID = loaded.NextStepID
	if s.nextStepID == 0 {
		s.nextStepID = 1
	}

	return nil
}

func (s *ThinkingState) save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := struct {
		Chains      map[string]*ThoughtChain `json:"chains"`
		ActiveChain string                   `json:"active_chain"`
		NextStepID  int                      `json:"next_step_id"`
	}{
		Chains:      s.Chains,
		ActiveChain: s.ActiveChain,
		NextStepID:  s.nextStepID,
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	// Ensure directory exists
	dir := s.persistPath[:len(s.persistPath)-len("/state.json")]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(s.persistPath, jsonData, 0644)
}

func (s *ThinkingState) getNextStepID() int {
	id := s.nextStepID
	s.nextStepID++
	return id
}

func handleStartThinking(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	title := v.String("title", fmt.Sprintf("Thought Chain %s", time.Now().Format("2006-01-02 15:04:05")))
	description := v.String("description", "")
	initialThought := v.String("initial_thought", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	chainID := fmt.Sprintf("chain_%d", time.Now().UnixNano())
	now := time.Now()

	chain := &ThoughtChain{
		ID:          chainID,
		Title:       title,
		Description: description,
		Steps:       []ThoughtStep{},
		CreatedAt:   now,
		UpdatedAt:   now,
		Status:      "active",
	}

	// Add initial thought if provided
	if initialThought != "" {
		thoughtType := v.String("thought_type", "observation")

		chain.Steps = append(chain.Steps, ThoughtStep{
			ID:          state.getNextStepID(),
			Thought:     initialThought,
			ThoughtType: thoughtType,
			Timestamp:   now,
		})
	}

	state.Chains[chainID] = chain
	state.ActiveChain = chainID

	if err := state.save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"chain_id":    chainID,
		"title":       title,
		"description": description,
		"status":      "active",
		"message":     "Started new thought chain. Use add_thought to continue reasoning.",
	})
}

func handleAddThought(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	thought := v.Required("thought")
	chainID := v.String("chain_id", "")
	thoughtType := v.String("thought_type", "")
	confidence := v.Float("confidence", 0)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Use active chain if not specified
	if chainID == "" {
		chainID = state.ActiveChain
	}
	if chainID == "" {
		return mcp.ErrorResult(fmt.Errorf("no active chain. Use start_thinking first or specify chain_id")), nil
	}

	chain, exists := state.Chains[chainID]
	if !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	if chain.Status != "active" {
		return mcp.ErrorResult(fmt.Errorf("chain is %s, cannot add thoughts", chain.Status)), nil
	}

	if thoughtType == "" {
		thoughtType = "reasoning"
	}

	now := time.Now()
	step := ThoughtStep{
		ID:          state.getNextStepID(),
		Thought:     thought,
		ThoughtType: thoughtType,
		Confidence:  confidence,
		Timestamp:   now,
	}

	chain.Steps = append(chain.Steps, step)
	chain.UpdatedAt = now

	if err := state.save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"step_id":      step.ID,
		"chain_id":     chainID,
		"step_number":  len(chain.Steps),
		"thought_type": thoughtType,
		"message":      fmt.Sprintf("Added thought step %d to chain", len(chain.Steps)),
	})
}

func handleGetChain(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chainID := v.String("chain_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	if chainID == "" {
		chainID = state.ActiveChain
	}
	if chainID == "" {
		return mcp.ErrorResult(fmt.Errorf("no active chain")), nil
	}

	chain, exists := state.Chains[chainID]
	if !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	return mcp.JSONResult(chain)
}

func handleListChains(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	statusFilter := v.String("status", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	var chains []map[string]any
	for _, chain := range state.Chains {
		if statusFilter != "" && chain.Status != statusFilter {
			continue
		}

		chains = append(chains, map[string]any{
			"id":         chain.ID,
			"title":      chain.Title,
			"status":     chain.Status,
			"step_count": len(chain.Steps),
			"created_at": chain.CreatedAt,
			"updated_at": chain.UpdatedAt,
			"is_active":  chain.ID == state.ActiveChain,
		})
	}

	return mcp.JSONResult(map[string]any{
		"chains":       chains,
		"active_chain": state.ActiveChain,
		"total":        len(chains),
	})
}

func handleSetActiveChain(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chainID := v.Required("chain_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if _, exists := state.Chains[chainID]; !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	state.ActiveChain = chainID

	if err := state.save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"active_chain": chainID,
		"message":      "Active chain updated",
	})
}

func handleCompleteChain(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chainID := v.String("chain_id", "")
	conclusion := v.String("conclusion", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if chainID == "" {
		chainID = state.ActiveChain
	}
	if chainID == "" {
		return mcp.ErrorResult(fmt.Errorf("no active chain")), nil
	}

	chain, exists := state.Chains[chainID]
	if !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	// Add conclusion as final step if provided
	if conclusion != "" {
		now := time.Now()
		chain.Steps = append(chain.Steps, ThoughtStep{
			ID:          state.getNextStepID(),
			Thought:     conclusion,
			ThoughtType: "conclusion",
			Confidence:  1.0,
			Timestamp:   now,
		})
		chain.UpdatedAt = now
	}

	chain.Status = "completed"

	// If completing active chain, clear active
	if state.ActiveChain == chainID {
		state.ActiveChain = ""
	}

	if err := state.save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"chain_id":    chainID,
		"status":      "completed",
		"total_steps": len(chain.Steps),
		"message":     "Thought chain completed",
	})
}

func handleBranchThought(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	parentID := v.RequiredInt("parent_step_id")
	thought := v.Required("thought")
	chainID := v.String("chain_id", "")
	thoughtType := v.String("thought_type", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if chainID == "" {
		chainID = state.ActiveChain
	}
	if chainID == "" {
		return mcp.ErrorResult(fmt.Errorf("no active chain")), nil
	}

	chain, exists := state.Chains[chainID]
	if !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	// Verify parent step exists
	found := false
	for _, step := range chain.Steps {
		if step.ID == parentID {
			found = true
			break
		}
	}
	if !found {
		return mcp.ErrorResult(fmt.Errorf("parent step not found: %d", parentID)), nil
	}

	if thoughtType == "" {
		thoughtType = "alternative"
	}

	now := time.Now()
	step := ThoughtStep{
		ID:          state.getNextStepID(),
		Thought:     thought,
		ThoughtType: thoughtType,
		Timestamp:   now,
		ParentID:    &parentID,
	}

	chain.Steps = append(chain.Steps, step)
	chain.UpdatedAt = now

	if err := state.save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"step_id":        step.ID,
		"parent_step_id": parentID,
		"chain_id":       chainID,
		"message":        fmt.Sprintf("Created branch from step %d", parentID),
	})
}

func handleDeleteChain(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chainID := v.Required("chain_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if _, exists := state.Chains[chainID]; !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	delete(state.Chains, chainID)

	if state.ActiveChain == chainID {
		state.ActiveChain = ""
	}

	if err := state.save(); err != nil {
		return nil, fmt.Errorf("failed to save state: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"chain_id": chainID,
		"message":  "Chain deleted",
	})
}

func handleSummarizeChain(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chainID := v.String("chain_id", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	if chainID == "" {
		chainID = state.ActiveChain
	}
	if chainID == "" {
		return mcp.ErrorResult(fmt.Errorf("no active chain")), nil
	}

	chain, exists := state.Chains[chainID]
	if !exists {
		return mcp.ErrorResult(fmt.Errorf("chain not found: %s", chainID)), nil
	}

	// Count thought types
	typeCounts := make(map[string]int)
	var avgConfidence float64
	confidenceCount := 0
	var branches int

	for _, step := range chain.Steps {
		typeCounts[step.ThoughtType]++
		if step.Confidence > 0 {
			avgConfidence += step.Confidence
			confidenceCount++
		}
		if step.ParentID != nil {
			branches++
		}
	}

	if confidenceCount > 0 {
		avgConfidence /= float64(confidenceCount)
	}

	// Build thought progression
	var progression []string
	for _, step := range chain.Steps {
		if step.ParentID == nil {
			progression = append(progression, fmt.Sprintf("[%s] %s", step.ThoughtType, truncate(step.Thought, 100)))
		}
	}

	return mcp.JSONResult(map[string]any{
		"chain_id":         chainID,
		"title":            chain.Title,
		"status":           chain.Status,
		"total_steps":      len(chain.Steps),
		"branches":         branches,
		"thought_types":    typeCounts,
		"avg_confidence":   avgConfidence,
		"duration_minutes": chain.UpdatedAt.Sub(chain.CreatedAt).Minutes(),
		"progression":      progression,
	})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
