package main

import (
	"fmt"
	"os"
	"os/exec"
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
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Error: Project ID must be provided as the first argument.")
		os.Exit(1)
	}
	projectID := os.Args[1]
	isDryRun := os.Getenv("DRY_RUN") == "true"
	configFile := ".github/seed-issues.yaml"
	repo := "flexinfer/flexinfer"

	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
		os.Exit(1)
	}

	var config Config
	if err := yaml.Unmarshal(yamlFile, &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error unmarshalling YAML: %v\n", err)
		os.Exit(1)
	}

	if isDryRun {
		fmt.Println("--- This is a dry run. No issues will be created. ---")
		fmt.Printf("Found %d issues to be created for project ID %s:\n", len(config.Issues), projectID)
		for _, issue := range config.Issues {
			fmt.Printf("- %s\n", issue.Title)
		}
		fmt.Println("--- End of dry run. ---")
		return
	}

	fmt.Printf("Starting issue creation for project ID: %s\n", projectID)

	for _, issue := range config.Issues {
		if issueExists(repo, issue.Title) {
			fmt.Printf("✅ Issue already exists: %s\n", issue.Title)
			continue
		}

		issueURL, err := createIssue(repo, issue)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating issue '%s': %v\n", issue.Title, err)
			continue
		}
		fmt.Printf("📄 Created issue: %s (%s)\n", issue.Title, issueURL)

		if err := addItemToProject(projectID, issueURL); err != nil {
			fmt.Fprintf(os.Stderr, "Error adding issue to project: %v\n", err)
		} else {
			fmt.Println("   └── Added to project board.")
		}
	}

	fmt.Println("🎉 Done seeding issues.")
}

func issueExists(repo, title string) bool {
	cmd := exec.Command("gh", "issue", "list", "-R", repo, "--state", "all", "--search", fmt.Sprintf(`in:title "%s"`, title), "--limit", "1", "--json", "number")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func createIssue(repo string, issue Issue) (string, error) {
	args := []string{
		"issue", "create", "-R", repo,
		"--title", issue.Title,
		"--body", issue.Body,
	}
	if len(issue.Labels) > 0 {
		args = append(args, "--label", strings.Join(issue.Labels, ","))
	}
	if issue.Milestone != "" {
		args = append(args, "--milestone", issue.Milestone)
	}
	if len(issue.Assignees) > 0 {
		args = append(args, "--assignee", strings.Join(issue.Assignees, ","))
	}

	cmd := exec.Command("gh", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh issue create failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func addItemToProject(projectID, issueURL string) error {
	cmd := exec.Command("gh", "project", "item-add", projectID, "--url", issueURL)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh project item-add failed: %w", err)
	}
	return nil
}
