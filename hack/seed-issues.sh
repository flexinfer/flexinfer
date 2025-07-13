#!/usr/bin/env bash
set -euo pipefail

CONFIG=".github/seed-issues.yaml"
REPO="flexinfer/flexinfer"
ORGANIZATION="flexinfer"
PROJECT_TITLE="flexinfer Roadmap"

gh auth status >/dev/null || {
  echo "Error: gh auth login is required. Please run 'gh auth login' with repo and project scopes."
  exit 1
}

# Find the project ID by its title within the organization
PROJECT_ID=$(gh project list --owner "$ORGANIZATION" --format=json | jq -r ".projects[] | select(.title == \"$PROJECT_TITLE\") | .id")

if [ -z "$PROJECT_ID" ]; then
  echo "Error: Project with title '${PROJECT_TITLE}' not found in organization '${ORGANIZATION}'."
  echo "Please ensure the project exists and the title is correct in this script."
  exit 1
}

echo "Found project '${PROJECT_TITLE}' with ID: ${PROJECT_ID}"

# Requires yq 4.x to be installed
# Use yq to loop through each issue and get its properties
for i in $(yq e '.issues | keys | .[]' "$CONFIG"); do
  title=$(yq e ".issues[$i].title" "$CONFIG")
  
  # Skip if an open or closed issue already has that title
  if gh issue list -R "$REPO" --state all --search "in:title \"$title\"" --json number | jq -e '.[0]' > /dev/null; then
    echo "✅ Issue already exists: $title"
    continue
  fi

  body=$(yq e ".issues[$i].body" "$CONFIG")
  labels=$(yq e ".issues[$i].labels | join(",")" "$CONFIG")
  milestone=$(yq e ".issues[$i].milestone // \"\"" "$CONFIG")
  assignees=$(yq e ".issues[$i].assignees | join(",") // \"\"" "$CONFIG")

  # Create the issue
  issue_url=$(gh issue create -R "$REPO" \
    --title "$title" \
    --body "$body" \
    --label "$labels" \
    ${milestone:+--milestone "$milestone"} \
    ${assignees:+--assignee "$assignees"})

  echo "📄 Created issue: $title ($issue_url)"

  # Add the new issue to the project board
  gh project item-add "$PROJECT_ID" --url "$issue_url"
  echo "   └── Added to project board."
done

echo "🎉 Done seeding issues."