package coordinator

import "testing"

func TestDecodeStructuredJSON_Fenced(t *testing.T) {
	var payload struct {
		Name  string `json:"name"`
		Steps []struct {
			ID string `json:"id"`
		} `json:"steps"`
	}

	raw := "```json\n{\"name\":\"workflow\",\"steps\":[{\"id\":\"1\"}]}\n```"
	if err := decodeStructuredJSON(raw, &payload); err != nil {
		t.Fatalf("decodeStructuredJSON: %v", err)
	}
	if payload.Name != "workflow" {
		t.Fatalf("Name = %q, want workflow", payload.Name)
	}
	if len(payload.Steps) != 1 || payload.Steps[0].ID != "1" {
		t.Fatalf("unexpected steps: %+v", payload.Steps)
	}
}

func TestDecodeStructuredJSON_ProseWrapped(t *testing.T) {
	var payload struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}

	raw := "Here is the result:\n\n{\"results\":[{\"id\":7}]}\n\nThanks."
	if err := decodeStructuredJSON(raw, &payload); err != nil {
		t.Fatalf("decodeStructuredJSON: %v", err)
	}
	if len(payload.Results) != 1 || payload.Results[0].ID != 7 {
		t.Fatalf("unexpected results: %+v", payload.Results)
	}
}
