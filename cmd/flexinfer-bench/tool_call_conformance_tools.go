package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/flexinfer/flexinfer/internal/agentloop"
)

// The tool_call_conformance catalogue: six operations tools whose schemas
// exercise what agent loops send (enums, integer and number bounds, regex
// patterns, arrays, optional fields, additionalProperties false). Parameters
// are kept as JSON text so the lane sees the property order written here;
// xgrammar emits object fields in schema order.

type tccTool struct {
	Name        string
	Description string
	Parameters  string
	prompt      func(k int) string
}

func (t tccTool) schema() map[string]any {
	tccSchemaOnce.Do(tccDecodeSchemas)
	return tccSchemas[t.Name]
}

var (
	tccSchemaOnce sync.Once
	tccSchemas    map[string]map[string]any
)

func tccDecodeSchemas() {
	tccSchemas = make(map[string]map[string]any, len(tccTools))
	for _, t := range tccTools {
		v, err := decodeJSONNumber([]byte(t.Parameters))
		if err != nil {
			panic(fmt.Sprintf("tool_call_conformance: %s parameters: %v", t.Name, err))
		}
		tccSchemas[t.Name] = v.(map[string]any)
	}
}

func tccToolByName(name string) (tccTool, bool) {
	for _, t := range tccTools {
		if t.Name == name {
			return t, true
		}
	}
	return tccTool{}, false
}

func tccToolDefs() []agentloop.ToolDef {
	defs := make([]agentloop.ToolDef, len(tccTools))
	for i, t := range tccTools {
		defs[i] = agentloop.ToolDef{Type: "function", Function: agentloop.ToolFunctionDef{
			Name: t.Name, Description: t.Description, Parameters: json.RawMessage(t.Parameters),
		}}
	}
	return defs
}

// tccEnvelopeSchema is the json_schema / structured_outputs call envelope:
// one of {"name": <const tool>, "arguments": <that tool's parameters>}.
func tccEnvelopeSchema() string {
	branches := make([]string, len(tccTools))
	for i, t := range tccTools {
		branches[i] = fmt.Sprintf(`{"type":"object","properties":{"name":{"const":%q},"arguments":%s},"required":["name","arguments"],"additionalProperties":false}`, t.Name, t.Parameters)
	}
	return `{"anyOf":[` + strings.Join(branches, ",") + `]}`
}

func tccJSONSystemPrompt() string {
	var b strings.Builder
	b.WriteString("You are an operations assistant. Answer every request with exactly one JSON object " +
		`{"name": <tool name>, "arguments": <arguments object>} that calls one of these tools, ` +
		"with arguments taken from the request. Output only the JSON object.\n")
	for _, t := range tccTools {
		fmt.Fprintf(&b, "- %s: %s Parameters (JSON Schema): %s\n", t.Name, t.Description, t.Parameters)
	}
	return b.String()
}

type tccScenario struct {
	Tool   string
	Prompt string
}

// tccScenarios returns n deterministic requests, cycling the tools so every
// session mixes them.
func tccScenarios(n int) []tccScenario {
	out := make([]tccScenario, n)
	for i := range out {
		t := tccTools[i%len(tccTools)]
		out[i] = tccScenario{Tool: t.Name, Prompt: t.prompt(i / len(tccTools))}
	}
	return out
}

// tccToolResult is the canned result appended after a good call, so the next
// turn sees a realistic tool message in its history.
func tccToolResult(name string, args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	raw, _ := json.Marshal(map[string]any{"ok": true, "tool": name, "fields": keys})
	return string(raw)
}

func pick[T any](values []T, k int) T { return values[((k%len(values))+len(values))%len(values)] }

var tccTools = []tccTool{
	{
		Name:        "get_weather",
		Description: "Current weather for a city.",
		Parameters:  `{"type":"object","properties":{"city":{"type":"string","minLength":1,"maxLength":60},"unit":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["city","unit"],"additionalProperties":false}`,
		prompt: func(k int) string {
			city := pick([]string{"Lisbon", "Osaka", "Denver", "Nairobi", "Reykjavik", "Buenos Aires", "Toronto", "Hanoi", "Perth", "Oslo", "Cairo", "Seattle", "Lima", "Prague", "Mumbai", "Anchorage", "Tbilisi"}, k)
			unit := pick([]string{"celsius", "fahrenheit"}, k/3)
			return fmt.Sprintf(pick([]string{
				"What's the weather in %s right now? Give me the temperature in %s.",
				"Check the current conditions for %s, %s please.",
				"I'm heading to %s later. How's the weather there, in %s?",
			}, k), city, unit)
		},
	},
	{
		Name:        "create_calendar_event",
		Description: "Create a calendar event.",
		Parameters:  `{"type":"object","properties":{"title":{"type":"string","minLength":1,"maxLength":80},"date":{"type":"string","pattern":"^20[0-9]{2}-[01][0-9]-[0-3][0-9]$"},"start_time":{"type":"string","pattern":"^([01][0-9]|2[0-3]):[0-5][0-9]$"},"duration_minutes":{"type":"integer","minimum":5,"maximum":480},"attendees":{"type":"array","items":{"type":"string","minLength":1},"maxItems":8}},"required":["title","date","start_time","duration_minutes"],"additionalProperties":false}`,
		prompt: func(k int) string {
			title := pick([]string{"Quarterly planning", "Design review", "1:1 with Priya", "Vendor call", "Retro", "Budget sync", "Hiring debrief", "Launch checklist"}, k)
			date := fmt.Sprintf("2026-%02d-%02d", 11+k%2, 1+(k*7)%28)
			start := pick([]string{"09:00", "10:30", "13:15", "15:45", "17:00"}, k)
			minutes := pick([]int{15, 30, 45, 60, 90}, k/2)
			with := ""
			if k%3 == 0 {
				with = " with " + pick([]string{"Ana and Ben", "Chloe", "Dev, Eli and Farah"}, k/3)
			}
			return fmt.Sprintf("Put a %d-minute meeting called %q on my calendar for %s at %s%s.", minutes, title, date, start, with)
		},
	},
	{
		Name:        "search_documents",
		Description: "Full-text search over an internal corpus.",
		Parameters:  `{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":200},"source":{"type":"string","enum":["wiki","tickets","runbooks","code"]},"top_k":{"type":"integer","minimum":1,"maximum":20}},"required":["query","source","top_k"],"additionalProperties":false}`,
		prompt: func(k int) string {
			query := pick([]string{"disk pressure eviction", "rotate the TLS certificate", "p99 latency regression", "GPU reset procedure", "flux reconcile stuck", "quarterly OKRs", "proxy first-byte budget", "backup restore drill", "longhorn replica rebuild"}, k)
			source := pick([]string{"wiki", "tickets", "runbooks", "code"}, k)
			return fmt.Sprintf("Search the %s for %q and give me the top %d results.", source, query, pick([]int{3, 5, 10, 15}, k/2))
		},
	},
	{
		Name:        "transfer_inventory",
		Description: "Move stock between warehouses.",
		Parameters:  `{"type":"object","properties":{"sku":{"type":"string","pattern":"^[A-Z]{3}-[0-9]{4}$"},"quantity":{"type":"integer","minimum":1,"maximum":10000},"from_warehouse":{"type":"string","enum":["austin","denver","reno","tulsa"]},"to_warehouse":{"type":"string","enum":["austin","denver","reno","tulsa"]}},"required":["sku","quantity","from_warehouse","to_warehouse"],"additionalProperties":false}`,
		prompt: func(k int) string {
			sites := []string{"Austin", "Denver", "Reno", "Tulsa"}
			from := k % 4
			to := (from + 1 + (k/4)%3) % 4
			sku := fmt.Sprintf("%s-%04d", pick([]string{"ABC", "KLM", "XQZ", "PRT", "DEV"}, k), (k*137+42)%10000)
			return fmt.Sprintf("Move %d units of SKU %s from the %s warehouse to %s.", pick([]int{12, 250, 4, 1800, 75, 9}, k), sku, sites[from], sites[to])
		},
	},
	{
		Name:        "set_thermostat",
		Description: "Set a thermostat zone.",
		Parameters:  `{"type":"object","properties":{"zone":{"type":"string","enum":["living_room","bedroom","office","garage"]},"temperature_c":{"type":"number","minimum":10,"maximum":30},"mode":{"type":"string","enum":["heat","cool","auto"]}},"required":["zone","temperature_c","mode"],"additionalProperties":false}`,
		prompt: func(k int) string {
			zone := pick([]string{"living room", "bedroom", "office", "garage"}, k)
			return fmt.Sprintf("Set the %s thermostat to %s°C in %s mode.", zone, pick([]string{"19", "21.5", "23", "17.5", "25", "20"}, k), pick([]string{"heat", "cool", "auto"}, k/2))
		},
	},
	{
		Name:        "lookup_order",
		Description: "Look up a customer order.",
		Parameters:  `{"type":"object","properties":{"order_id":{"type":"string","pattern":"^ORD-[0-9]{6}$"},"include_items":{"type":"boolean"}},"required":["order_id","include_items"],"additionalProperties":false}`,
		prompt: func(k int) string {
			id := fmt.Sprintf("ORD-%06d", 100000+(k*7919)%900000)
			if k%2 == 0 {
				return fmt.Sprintf("Look up order %s and list its line items.", id)
			}
			return fmt.Sprintf("What's the status of order %s? I don't need the line items.", id)
		},
	},
}
