package orchestra

import (
	"fmt"
	"strings"
)

// routerClassifyPrompt generates the system prompt for domain classification.
func routerClassifyPrompt(domains []SubAgent) string {
	var b strings.Builder
	b.WriteString("You are a query router. Classify the user's query into one or more domains.\n\n")
	b.WriteString("Available domains:\n")
	for _, d := range domains {
		fmt.Fprintf(&b, "- %s: %s\n", d.Name, d.Description)
	}
	b.WriteString("\nRespond with ONLY a JSON object: {\"domains\": [\"domain1\", \"domain2\"]}\n")
	b.WriteString("Select the minimum set of domains needed to answer the query.\n")
	b.WriteString("If the query doesn't match any domain, respond: {\"domains\": []}\n")
	return b.String()
}

// routerSynthesizePrompt generates the system prompt for multi-domain synthesis.
func routerSynthesizePrompt() string {
	return `You are a synthesis agent. Combine the following domain results into a single coherent answer.
Be concise and focus on the most relevant information.
If results from different domains conflict, note the discrepancy.
Do not add information not present in the domain results.`
}

// subAgentSystemPrompt generates the system prompt for a domain-specific subagent.
func subAgentSystemPrompt(agent SubAgent) string {
	if agent.SystemPrompt != "" {
		return agent.SystemPrompt
	}
	return fmt.Sprintf(
		"You are a %s specialist. Use the available tools to answer the user's query.\n"+
			"Be concise and return only the relevant information.\n"+
			"If a tool call fails, report the error and continue with other tools.",
		agent.Name,
	)
}
