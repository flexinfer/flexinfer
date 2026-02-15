package widgets

import (
	"strings"
	"testing"
)

func TestComputeColWidths(t *testing.T) {
	tests := []struct {
		name    string
		table   Table
		wantMin []int // minimum expected widths
	}{
		{
			name: "headers only",
			table: Table{
				Headers: []string{"Name", "Age"},
				Rows:    nil,
			},
			wantMin: []int{4, 3},
		},
		{
			name: "rows wider than headers",
			table: Table{
				Headers: []string{"ID", "Name"},
				Rows:    [][]string{{"12345", "Alice"}},
			},
			wantMin: []int{5, 5},
		},
		{
			name: "with total width distributes extra",
			table: Table{
				Headers: []string{"A", "B"},
				Rows:    nil,
				Width:   20,
			},
			wantMin: []int{1, 1}, // at least header width; extra distributed
		},
		{
			name: "short rows padded",
			table: Table{
				Headers: []string{"A", "B", "C"},
				Rows:    [][]string{{"x"}}, // fewer columns than headers
			},
			wantMin: []int{1, 1, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.table.computeColWidths()
			if len(got) != len(tt.wantMin) {
				t.Fatalf("got %d columns, want %d", len(got), len(tt.wantMin))
			}
			for i, min := range tt.wantMin {
				if got[i] < min {
					t.Errorf("col %d width %d < min %d", i, got[i], min)
				}
			}
		})
	}
}

func TestTableRenderEmpty(t *testing.T) {
	tbl := Table{Headers: nil, Rows: nil}
	got := tbl.Render()
	if got != "" {
		t.Errorf("empty table should return empty string, got %q", got)
	}
}

func TestTableRenderContainsHeaders(t *testing.T) {
	tbl := Table{
		Headers: []string{"Name", "Value"},
		Rows:    [][]string{{"foo", "42"}},
	}
	got := tbl.Render()
	if !strings.Contains(got, "NAME") {
		t.Error("expected uppercased header 'NAME'")
	}
	if !strings.Contains(got, "VALUE") {
		t.Error("expected uppercased header 'VALUE'")
	}
}

func TestTableRenderContainsSeparator(t *testing.T) {
	tbl := Table{
		Headers: []string{"A"},
		Rows:    [][]string{{"x"}},
	}
	got := tbl.Render()
	if !strings.Contains(got, "─") {
		t.Error("expected separator line with ─ characters")
	}
}

func TestTableRenderContainsData(t *testing.T) {
	tbl := Table{
		Headers: []string{"Name"},
		Rows:    [][]string{{"hello"}, {"world"}},
	}
	got := tbl.Render()
	if !strings.Contains(got, "hello") {
		t.Error("expected data 'hello'")
	}
	if !strings.Contains(got, "world") {
		t.Error("expected data 'world'")
	}
}

func TestSumInts(t *testing.T) {
	tests := []struct {
		vals []int
		want int
	}{
		{nil, 0},
		{[]int{1, 2, 3}, 6},
		{[]int{0}, 0},
		{[]int{-1, 1}, 0},
	}
	for _, tt := range tests {
		got := sumInts(tt.vals)
		if got != tt.want {
			t.Errorf("sumInts(%v) = %d, want %d", tt.vals, got, tt.want)
		}
	}
}
