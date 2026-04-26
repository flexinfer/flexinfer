package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// jsonField marshals v to a JSON string for storage. Empty slices/maps round
// trip as "[]" / "{}" respectively, never NULL — callers and the schema rely
// on non-NULL JSON columns.
func jsonField(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return string(b), nil
}

// jsonInto unmarshals a TEXT column into dest. Empty strings are treated as a
// fresh zero-value of dest's type so callers don't need to special-case nullable
// JSON columns.
func jsonInto(s string, dest any) error {
	if s == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(s), dest); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	return nil
}

// timeRFC3339 serialises t as a UTC RFC3339Nano string for SQLite storage.
// All timestamps in the hive schema are TEXT in this format.
func timeRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime parses an RFC3339(Nano) timestamp from a SQLite TEXT column.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	// Tolerate the no-fraction form for migrations of older data.
	return time.Parse(time.RFC3339, s)
}

// nullableTime parses a nullable TEXT timestamp into a *time.Time.
func nullableTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// nullableInt64 unwraps sql.NullInt64.
func nullableInt64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullableString unwraps sql.NullString to "" for non-pointer fields, or *string
// when the caller wants to distinguish absent from empty.
func nullableString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}
