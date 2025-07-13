package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v3"
)

// Issue defines the structure for an issue in the YAML file.
type Issue struct {
	Title     string   `yaml:"title"`
	Body      string   `yaml:"body"`
	Labels    []string `yaml:"labels"`
	Milestone string   `yaml:"milestone"`
	Assignees []string `yaml:"assignees"`
}

// Config holds the list of issues from the YAML file.
type Config struct {
	Issues []Issue `yaml:"issues"`
}

// GhProject defines the structure for a GitHub project from the gh CLI JSON output.
type GhProject struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// GhProjectList holds a list of GitHub projects.
type GhProjectList struct {
	Projects []GhProject `json:"projects"`
}

func main() {
	// --- Configuration ---
	isDryRun := os.Getenv("DRY_RUN") == "true"
	configFile := ".github/seed-issues.yaml"
	repo := "flexinfer/flexinfer"
	organization := "flexinfer"
	projectTitle := "flexinfer Roadmap"

	// --- Read and parse the YAML config file ---
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

	// --- Handle Dry Run ---
	if isDryRun {
		fmt.Println("--- This is a dry run. No issues will be created. ---")
		fmt.Printf("Found %d issues to be created:\n", len(config.Issues))
		for _, issue := range config.Issues {
			fmt.Printf("- %s\n", issue.Title)
		}
		fmt.Println("--- End of dry run. ---")
		return // Exit successfully
	}

	// --- Find the Project ID ---
	projectID, err := findProjectID(organization, projectTitle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding project: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Found project '%s' with ID: %s\n", projectTitle, projectID)

	// --- Create Issues ---
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

func findProjectID(organization, projectTitle string) (string, error) {
	cmd := exec.Command("gh", "project", "list", "--owner", organization, "--format", "json")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gh project list failed: %w", err)
	}

	var projectList GhProjectList
	if err := json.Unmarshal(out.Bytes(), &projectList); err != nil {
		return "", fmt.Errorf("failed to parse gh project list JSON: %w", err)
	}

	for _, p := range projectList.Projects {
		if p.Title == projectTitle {
			return p.ID, nil
		}
	}

	return "", fmt.Errorf("project with title '%s' not found in organization '%s'", projectTitle, organization)
}

func issueExists(repo, title string) bool {
	// Using --limit 1 is more efficient
	cmd := exec.Command("gh", "issue", "list", "-R", repo, "--state", "all", "--search", fmt.Sprintf(`in:title "%s"`, title), "--limit", "1", "--json", "number")
	// We only care if the command succeeds, not what it outputs.
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
