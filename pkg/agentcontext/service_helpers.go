package agentcontext

// getBool extracts a bool from an interface value, returning def if not a bool.
func getBool(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

// toFloat extracts a float64 from an interface value (supports float64, int, int64).
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
