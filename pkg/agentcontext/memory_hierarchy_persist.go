package agentcontext

import (
	"context"
	"fmt"
)

// =========================================================================
// Persistence Layer (Phase 1.3)
// =========================================================================

// MemoryPersistenceConfig holds Qdrant client for memory persistence
type MemoryPersistenceConfig struct {
	MemoryQdrant *QdrantClient
	EmbedModel   string
	VectorSize   int
}

// persistedMemoryHierarchy wraps MemoryHierarchy with Qdrant persistence
type persistedMemoryHierarchy struct {
	*MemoryHierarchy
	cfg *MemoryPersistenceConfig
}

// SetPersistence configures Qdrant persistence for memory hierarchy
func (mh *MemoryHierarchy) SetPersistence(cfg *MemoryPersistenceConfig) *persistedMemoryHierarchy {
	return &persistedMemoryHierarchy{
		MemoryHierarchy: mh,
		cfg:             cfg,
	}
}

// PersistItem saves a memory item to Qdrant
func (pmh *persistedMemoryHierarchy) PersistItem(ctx context.Context, item *MemoryItem, vector []float64) error {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil // No persistence configured
	}

	// Ensure collection exists
	vectorSize := pmh.cfg.VectorSize
	if vectorSize <= 0 {
		vectorSize = 4 // Minimal size if embeddings not configured
	}
	if err := pmh.cfg.MemoryQdrant.EnsureCollection(ctx, vectorSize); err != nil {
		return fmt.Errorf("ensure memory collection: %w", err)
	}

	// Use zero vector if not provided
	if len(vector) == 0 {
		vector = make([]float64, vectorSize)
	}

	point := Point{
		ID:      item.ID,
		Vector:  vector,
		Payload: MemoryItemToPayload(*item, pmh.cfg.EmbedModel),
	}

	if err := pmh.cfg.MemoryQdrant.Upsert(ctx, []Point{point}, true); err != nil {
		return fmt.Errorf("persist memory item: %w", err)
	}

	return nil
}

// DeletePersistedItem removes a memory item from Qdrant
func (pmh *persistedMemoryHierarchy) DeletePersistedItem(ctx context.Context, id string) error {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil
	}
	return pmh.cfg.MemoryQdrant.Delete(ctx, []string{id})
}

// LoadMemoryFromQdrant loads all memory items from Qdrant into memory
func (pmh *persistedMemoryHierarchy) LoadMemoryFromQdrant(ctx context.Context) error {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil
	}

	exists, err := pmh.cfg.MemoryQdrant.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("check memory collection: %w", err)
	}
	if !exists {
		return nil
	}

	points, err := pmh.cfg.MemoryQdrant.ScrollPoints(ctx, nil, 10000, false)
	if err != nil {
		return fmt.Errorf("load memory items: %w", err)
	}

	pmh.mu.Lock()
	defer pmh.mu.Unlock()

	for _, p := range points {
		item, err := PayloadToMemoryItem(p.Payload)
		if err != nil || item == nil {
			continue
		}

		// Add to appropriate tier
		pmh.addToTier(item)
		pmh.indexItem(item)
	}

	return nil
}

// AddItemWithPersistence persists an item to Qdrant first, then adds it to
// the in-memory hierarchy. Persist-first ensures in-memory state never
// diverges from Qdrant.
func (pmh *persistedMemoryHierarchy) AddItemWithPersistence(ctx context.Context, item *MemoryItem, vector []float64) error {
	// Persist to Qdrant first.
	if err := pmh.PersistItem(ctx, item, vector); err != nil {
		return err
	}

	// Add to in-memory hierarchy.
	if err := pmh.AddItem(item); err != nil {
		return err
	}

	return nil
}

// UpdateItemWithPersistence updates an item and persists changes
func (pmh *persistedMemoryHierarchy) UpdateItemWithPersistence(ctx context.Context, item *MemoryItem, vector []float64) error {
	if err := pmh.UpdateItem(item); err != nil {
		return err
	}
	return pmh.PersistItem(ctx, item, vector)
}

// DeleteItemWithPersistence removes from Qdrant first, then deletes in-memory.
// Qdrant-first for deletes ensures in-memory state never diverges.
func (pmh *persistedMemoryHierarchy) DeleteItemWithPersistence(ctx context.Context, id string) error {
	if err := pmh.DeletePersistedItem(ctx, id); err != nil {
		return err
	}
	return pmh.DeleteItem(id)
}

// PromoteItemWithPersistence persists the promoted state to Qdrant first,
// then promotes in-memory. If Qdrant fails, the item stays in its original
// tier and gets a _promotion_failed metadata tag.
func (pmh *persistedMemoryHierarchy) PromoteItemWithPersistence(ctx context.Context, id string) error {
	// Snapshot the promoted state without modifying in-memory.
	snapshot, err := pmh.snapshotPromoted(id)
	if err != nil {
		return err
	}

	// Persist promoted snapshot to Qdrant first.
	if err := pmh.PersistItem(ctx, snapshot, nil); err != nil {
		pmh.tagPromotionFailed(id)
		return fmt.Errorf("persist promoted item: %w", err)
	}

	// Now apply in-memory promotion (should succeed since item exists).
	return pmh.PromoteItem(id)
}

// DemoteItemWithPersistence persists the demoted state to Qdrant first,
// then demotes in-memory. If Qdrant fails, the item stays in its original tier.
func (pmh *persistedMemoryHierarchy) DemoteItemWithPersistence(ctx context.Context, id string) error {
	// Snapshot the demoted state without modifying in-memory.
	snapshot, err := pmh.snapshotDemoted(id)
	if err != nil {
		return err
	}

	// Persist demoted snapshot to Qdrant first.
	if err := pmh.PersistItem(ctx, snapshot, nil); err != nil {
		return fmt.Errorf("persist demoted item: %w", err)
	}

	// Now apply in-memory demotion.
	return pmh.DemoteItem(id)
}

// CompressItemWithPersistence compresses an item and persists the change
func (pmh *persistedMemoryHierarchy) CompressItemWithPersistence(ctx context.Context, id string) error {
	if err := pmh.CompressItem(id); err != nil {
		return err
	}

	// Get the item to persist
	pmh.mu.RLock()
	item := pmh.findItem(id)
	pmh.mu.RUnlock()

	if item != nil {
		return pmh.PersistItem(ctx, item, nil)
	}
	return nil
}

// MergeItemsWithPersistence merges items and persists changes
func (pmh *persistedMemoryHierarchy) MergeItemsWithPersistence(ctx context.Context, ids []string, newTitle string, vector []float64) (*MemoryItem, error) {
	merged, err := pmh.MergeItems(ids, newTitle)
	if err != nil {
		return nil, err
	}

	// Persist the merged item
	if err := pmh.PersistItem(ctx, merged, vector); err != nil {
		return merged, fmt.Errorf("persist merged item: %w", err)
	}

	// Persist archived items
	for _, id := range ids {
		pmh.mu.RLock()
		item := pmh.findItem(id)
		pmh.mu.RUnlock()
		if item != nil && item.Status == MemoryItemStatusArchived {
			if err := pmh.PersistItem(ctx, item, nil); err != nil {
				// Non-fatal
				fmt.Printf("warning: failed to persist archived item: %v\n", err)
			}
		}
	}

	return merged, nil
}

// snapshotPromoted creates a copy of the item with its promoted tier and expiry
// without modifying the original in-memory item.
func (pmh *persistedMemoryHierarchy) snapshotPromoted(id string) (*MemoryItem, error) {
	pmh.mu.RLock()
	defer pmh.mu.RUnlock()

	item := pmh.findItem(id)
	if item == nil {
		return nil, fmt.Errorf("memory item not found: %s", id)
	}

	var newTier MemoryTier
	switch item.Tier {
	case MemoryTierWorking:
		newTier = MemoryTierShortTerm
	case MemoryTierShortTerm:
		newTier = MemoryTierLongTerm
	case MemoryTierLongTerm:
		return nil, fmt.Errorf("item is already in long-term memory")
	}

	snapshot := *item
	snapshot.Tier = newTier
	return &snapshot, nil
}

// snapshotDemoted creates a copy of the item with its demoted tier
// without modifying the original in-memory item.
func (pmh *persistedMemoryHierarchy) snapshotDemoted(id string) (*MemoryItem, error) {
	pmh.mu.RLock()
	defer pmh.mu.RUnlock()

	item := pmh.findItem(id)
	if item == nil {
		return nil, fmt.Errorf("memory item not found: %s", id)
	}

	var newTier MemoryTier
	switch item.Tier {
	case MemoryTierWorking:
		return nil, fmt.Errorf("cannot demote from working memory")
	case MemoryTierShortTerm:
		newTier = MemoryTierWorking
	case MemoryTierLongTerm:
		newTier = MemoryTierShortTerm
	}

	snapshot := *item
	snapshot.Tier = newTier
	return &snapshot, nil
}

// tagPromotionFailed adds _promotion_failed metadata to an item.
func (pmh *persistedMemoryHierarchy) tagPromotionFailed(id string) {
	pmh.mu.Lock()
	defer pmh.mu.Unlock()
	item := pmh.findItem(id)
	if item == nil {
		return
	}
	if item.Metadata == nil {
		item.Metadata = make(map[string]any)
	}
	item.Metadata["_promotion_failed"] = true
}

// SearchMemorySemantic performs semantic search for memory items
func (pmh *persistedMemoryHierarchy) SearchMemorySemantic(ctx context.Context, vector []float64, limit int, tier MemoryTier, namespace string) ([]*MemoryItem, error) {
	if pmh.cfg == nil || pmh.cfg.MemoryQdrant == nil {
		return nil, fmt.Errorf("no persistence configured for semantic search")
	}

	// Build filter
	var conds []any
	if tier != "" {
		conds = append(conds, Match("tier", string(tier)))
	}
	if namespace != "" {
		conds = append(conds, Match("namespace", namespace))
	}
	// Exclude expired/archived items
	conds = append(conds, map[string]any{
		"key": "status",
		"match": map[string]any{
			"any": []string{string(MemoryItemStatusActive), string(MemoryItemStatusCompressed)},
		},
	})

	filter := FilterMust(conds...)

	// Search
	type searchResult struct {
		ID      string         `json:"id"`
		Score   float64        `json:"score"`
		Payload map[string]any `json:"payload"`
	}

	path := fmt.Sprintf("/collections/%s/points/search", pmh.cfg.MemoryQdrant.collection)
	body := map[string]any{
		"vector":       vector,
		"limit":        limit,
		"with_payload": true,
		"filter":       filter,
	}

	var resp struct {
		Result []searchResult `json:"result"`
	}
	if err := pmh.cfg.MemoryQdrant.doJSON(ctx, "POST", path, body, &resp); err != nil {
		return nil, err
	}

	items := make([]*MemoryItem, 0, len(resp.Result))
	for _, hit := range resp.Result {
		item, err := PayloadToMemoryItem(hit.Payload)
		if err != nil || item == nil {
			continue
		}
		items = append(items, item)
	}

	return items, nil
}
