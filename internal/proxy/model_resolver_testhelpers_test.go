package proxy

import "time"

// expireCachesForTest makes the next refresh of both caches run, taking the
// same locks the refreshes do so it is safe beside concurrent readers.
func (r *ModelResolver) expireCachesForTest() {
	r.serviceLabelCacheMu.Lock()
	r.lastCacheRefresh = time.Time{}
	r.serviceLabelCacheMu.Unlock()
	r.modelAliasCacheMu.Lock()
	r.lastAliasRefresh = time.Time{}
	r.modelAliasCacheMu.Unlock()
}

func (r *ModelResolver) lookupFirstForTest(label string) (string, bool) {
	v, ok := r.labelSnapshot().first[label]
	return v, ok
}

func (r *ModelResolver) lookupGroupForTest(label string) ([]string, bool) {
	v, ok := r.labelSnapshot().groups[label]
	return v, ok
}

func (r *ModelResolver) lookupGroupModelsForTest(model string) ([]string, bool) {
	v, ok := r.labelSnapshot().groupModels[model]
	return v, ok
}

// addGroupModelForTest publishes a copy of the current snapshot with one
// more reverse-index entry, the way a refresh would.
func (r *ModelResolver) addGroupModelForTest(model string, members []string) {
	r.serviceLabelCacheMu.Lock()
	defer r.serviceLabelCacheMu.Unlock()
	old := r.labelSnapshot()
	next := &labelSnapshot{first: old.first, groups: old.groups, groupModels: make(map[string][]string, len(old.groupModels)+1)}
	for k, v := range old.groupModels {
		next.groupModels[k] = v
	}
	next.groupModels[model] = members
	r.labels.Store(next)
}
