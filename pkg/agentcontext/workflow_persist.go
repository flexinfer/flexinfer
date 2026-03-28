// workflow_persist.go — Qdrant persistence layer for workflows and definitions.
package agentcontext

import (
	"context"
	"fmt"
)

// WorkflowPersistenceConfig holds Qdrant clients for workflow persistence
type WorkflowPersistenceConfig struct {
	WorkflowsQdrant    *QdrantClient
	WorkflowDefsQdrant *QdrantClient
}

// persistedWorkflowEngine wraps WorkflowEngine with Qdrant persistence
type persistedWorkflowEngine struct {
	*WorkflowEngine
	cfg *WorkflowPersistenceConfig
}

// SetPersistence configures Qdrant persistence for the workflow engine
func (e *WorkflowEngine) SetPersistence(cfg *WorkflowPersistenceConfig) *persistedWorkflowEngine {
	return &persistedWorkflowEngine{
		WorkflowEngine: e,
		cfg:            cfg,
	}
}

// dummyWorkflowVector is used for workflows (no semantic search needed)
var dummyWorkflowVector = []float64{0, 0, 0, 0}

// PersistWorkflow saves a workflow to Qdrant
func (pwe *persistedWorkflowEngine) PersistWorkflow(ctx context.Context, wf *Workflow) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowsQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists (using minimal vector size)
	if err := pwe.cfg.WorkflowsQdrant.EnsureCollection(ctx, 4); err != nil {
		return fmt.Errorf("ensure workflows collection: %w", err)
	}

	point := Point{
		ID:      wf.ID,
		Vector:  dummyWorkflowVector,
		Payload: WorkflowToPayload(*wf),
	}

	if err := pwe.cfg.WorkflowsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist workflow: %w", err)
	}

	return nil
}

// PersistDefinition saves a workflow definition to Qdrant
func (pwe *persistedWorkflowEngine) PersistDefinition(ctx context.Context, def *WorkflowDefinition) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowDefsQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists
	if err := pwe.cfg.WorkflowDefsQdrant.EnsureCollection(ctx, 4); err != nil {
		return fmt.Errorf("ensure workflow definitions collection: %w", err)
	}

	point := Point{
		ID:      def.ID,
		Vector:  dummyWorkflowVector,
		Payload: WorkflowDefinitionToPayload(*def),
	}

	if err := pwe.cfg.WorkflowDefsQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist workflow definition: %w", err)
	}

	return nil
}

// DeletePersistedWorkflow removes a workflow from Qdrant
func (pwe *persistedWorkflowEngine) DeletePersistedWorkflow(ctx context.Context, id string) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowsQdrant == nil {
		return nil
	}
	return pwe.cfg.WorkflowsQdrant.Delete(ctx, []string{id})
}

// DeletePersistedDefinition removes a workflow definition from Qdrant
func (pwe *persistedWorkflowEngine) DeletePersistedDefinition(ctx context.Context, id string) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowDefsQdrant == nil {
		return nil
	}
	return pwe.cfg.WorkflowDefsQdrant.Delete(ctx, []string{id})
}

// LoadWorkflowsFromQdrant loads all workflows from Qdrant into memory
func (pwe *persistedWorkflowEngine) LoadWorkflowsFromQdrant(ctx context.Context) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowsQdrant == nil {
		return nil
	}

	exists, err := pwe.cfg.WorkflowsQdrant.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("check workflows collection: %w", err)
	}
	if !exists {
		return nil
	}

	points, err := pwe.cfg.WorkflowsQdrant.ScrollPoints(ctx, nil, 10000, false)
	if err != nil {
		return fmt.Errorf("load workflows: %w", err)
	}

	pwe.mu.Lock()
	defer pwe.mu.Unlock()

	for _, p := range points {
		wf, err := PayloadToWorkflow(p.Payload)
		if err != nil || wf == nil {
			continue
		}
		pwe.workflows[wf.ID] = wf
		pwe.events[wf.ID] = []WorkflowEvent{} // Events not persisted
	}

	return nil
}

// LoadDefinitionsFromQdrant loads all workflow definitions from Qdrant into memory
func (pwe *persistedWorkflowEngine) LoadDefinitionsFromQdrant(ctx context.Context) error {
	if pwe.cfg == nil || pwe.cfg.WorkflowDefsQdrant == nil {
		return nil
	}

	exists, err := pwe.cfg.WorkflowDefsQdrant.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("check workflow definitions collection: %w", err)
	}
	if !exists {
		return nil
	}

	points, err := pwe.cfg.WorkflowDefsQdrant.ScrollPoints(ctx, nil, 10000, false)
	if err != nil {
		return fmt.Errorf("load workflow definitions: %w", err)
	}

	pwe.mu.Lock()
	defer pwe.mu.Unlock()

	for _, p := range points {
		def, err := PayloadToWorkflowDefinition(p.Payload)
		if err != nil || def == nil {
			continue
		}
		pwe.definitions[def.ID] = def
	}

	return nil
}

// RegisterDefinitionWithPersistence registers and persists a workflow definition
func (pwe *persistedWorkflowEngine) RegisterDefinitionWithPersistence(ctx context.Context, def *WorkflowDefinition) error {
	if err := pwe.RegisterDefinition(def); err != nil {
		return err
	}
	return pwe.PersistDefinition(ctx, def)
}

// StartWorkflowWithPersistence starts a workflow and persists it
func (pwe *persistedWorkflowEngine) StartWorkflowWithPersistence(ctx context.Context, definitionID, sessionID, agentID string, input map[string]any) (*Workflow, error) {
	wf, err := pwe.StartWorkflow(ctx, definitionID, sessionID, agentID, input)
	if err != nil {
		return nil, err
	}

	// Persist the initial workflow state
	if err := pwe.PersistWorkflow(ctx, wf); err != nil {
		// Non-fatal - workflow is running in memory
		fmt.Printf("warning: failed to persist workflow: %v\n", err)
	}

	return wf, nil
}

// CheckpointWorkflow saves the current workflow state to Qdrant
// This should be called after each step completion
func (pwe *persistedWorkflowEngine) CheckpointWorkflow(ctx context.Context, workflowID string) error {
	pwe.mu.RLock()
	wf, ok := pwe.workflows[workflowID]
	pwe.mu.RUnlock()

	if !ok {
		return fmt.Errorf("workflow not found: %s", workflowID)
	}

	return pwe.PersistWorkflow(ctx, wf)
}

// ResumeInProgressWorkflows resumes workflows that were in-progress when service restarted
func (pwe *persistedWorkflowEngine) ResumeInProgressWorkflows(ctx context.Context) error {
	pwe.mu.RLock()
	var toResume []*Workflow
	for _, wf := range pwe.workflows {
		if wf.Status == WorkflowStatusRunning || wf.Status == WorkflowStatusWaiting {
			toResume = append(toResume, wf)
		}
	}
	pwe.mu.RUnlock()

	for _, wf := range toResume {
		// Only resume running workflows, not waiting ones (they need user action)
		if wf.Status == WorkflowStatusRunning {
			go pwe.executeWorkflow(ctx, wf)
		}
	}

	return nil
}
