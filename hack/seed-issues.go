package main

import (
	"fmt"
	os"
	os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

type Issue struct {
	Title     string   `yaml:"title"`
	Body      string   `yaml:"body"`
	Labels    []string `yaml:"labels"`
	Milestone string   `yaml:"milestone"`
	Assignees []string `yaml:"assignees"`
}

type Config struct {
	Issues []Issue `yaml:"issues"`
}

func main() {
	isDryRun := os.Getenv("DRY_RUN") == "true"
	configFile := ".github/seed-issues.yaml"
	repo := "flexinfer/flexinfer"
	organization := "flexinfer"
	projectTitle := "flexinfer Roadmap"

	// Read the config file
	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Printf("Error reading config file: %v\n", err)
		os.Exit(1)
	}

	var config Config
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		fmt.Printf("Error unmarshalling config file: %v\n", err)
		os.Exit(1)
	}

	if isDryRun {
		fmt.Println("--- This is a dry run. No issues will be created. ---")
		fmt.Printf("Found %d issues to be created:\n", len(config.Issues))
		for _, issue := range config.Issues {
			fmt.Printf("- %s\n", issue.Title)
		}
		fmt.Println("--- End of dry run. ---")
		os.Exit(0)
	}

	// Get the project ID
	projectIDCmd := fmt.Sprintf("gh project list --owner %s --format=json | jq -r '.projects[] | select(.title == \"%s\") | .id'", organization, projectTitle)
	projectIDBytes, err := exec.Command("bash", "-c", projectIDCmd).Output()
	if err != nil {
		fmt.Printf("Error getting project ID: %v\n", err)
		os.Exit(1)
	}
	projectID := strings.TrimSpace(string(projectIDBytes))

	if projectID == "" {
		fmt.Printf("Error: Project with title '%s' not found in organization '%s'.\n", projectTitle, organization)
		os.Exit(1)
	}

	fmt.Printf("Found project '%s' with ID: %s\n", projectTitle, projectID)

	// Create the issues
	for _, issue := range config.Issues {
		// Check if the issue already exists
		checkCmd := fmt.Sprintf("gh issue list -R %s --state all --search 'in:title \"%s\"' --json number | jq -e '.[0]'", repo, issue.Title)
		if err := exec.Command("bash", "-c", checkCmd).Run() == nil {
			fmt.Printf("✅ Issue already exists: %s\n", issue.Title)
			continue
		}

		// Create the issue
		createCmd := []string{
			"issue", "create", "-R", repo,
			"--title", issue.Title,
			"--body", issue.Body,
		}
		if len(issue.Labels) > 0 {
			createCmd = append(createCmd, "--label", strings.Join(issue.Labels, ","))
		}
		if issue.Milestone != "" {
			createCmd = append(createCmd, "--milestone", issue.Milestone)
		}
		if len(issue.Assignees) > 0 {
			createCmd = append(createCmd, "--assignee", strings.Join(issue.Assignees, ","))
		}

		issueURLBytes, err := exec.Command("gh", createCmd...).Output()
		if err != nil {
			fmt.Printf("Error creating issue '%s': %v\n", issue.Title, err)
			continue
		}
		issueURL := strings.TrimSpace(string(issueURLBytes))
		fmt.Printf("📄 Created issue: %s (%s)\n", issue.Title, issueURL)

		// Add the issue to the project
		addCmd := []string{
			"project", "item-add", projectID,
			"--url", issueURL,
		}
\t	if err := exec.Command("gh", addCmd...).Run(); err != nil {
			fmt.Printf("Error adding issue to project: %v\n", err)
		} else {
			fmt.Println("   └── Added to project board.")
		}
	}

	fmt.Println("🎉 Done seeding issues.")
}