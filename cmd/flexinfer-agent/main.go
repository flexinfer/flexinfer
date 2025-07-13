package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/flexinfer/flexinfer/agents/agent"
)

func main() {
	interval := flag.Duration("interval", 30*time.Second, "How often to re-probe hardware.")
	metricsPort := flag.Int("metrics-port", 9100, "Prometheus scrape port.")
	labelPrefix := flag.String("label-prefix", "flexinfer.ai/", "Customize if conflicts with other labelers.")
	flag.Parse()

	fmt.Printf("Starting FlexInfer agent with configuration:\n")
	fmt.Printf("  Interval: %s\n", *interval)
	fmt.Printf("  Metrics Port: %d\n", *metricsPort)
	fmt.Printf("  Label Prefix: %s\n", *labelPrefix)

	// Placeholder for agent logic
	// In a real scenario, we would initialize the agent and start its loop.
	// For now, we just print a message.
	fmt.Println("FlexInfer Agent initialized (placeholder).")

	// Create a new agent
	nodeAgent, err := agent.NewAgent(*labelPrefix)
	if err != nil {
		panic(err)
	}

	// Start the agent's main loop
	for {
		fmt.Println("Probing node...")
		if err := nodeAgent.ProbeAndLabel(); err != nil {
			fmt.Printf("Error probing and labeling node: %v\n", err)
		}
		time.Sleep(*interval)
	}
}
