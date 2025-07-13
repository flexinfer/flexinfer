#!/usr/bin/env bash
set -euo pipefail

CONFIG=".github/seed-issues.yaml"
REPO="flexinfer/flexinfer"

gh auth status >/dev/null || {
  echo "  Run 'gh auth login' first (needs repo & project scopes)"; exit 1;
}

# Requires yq 
 4.x
yq -o=json '.[]' "$CONFIG" | while read -r row; do
  title=$(echo "$row" | jq -r '.title')
  # Skip if an open or closed issue already has that title
  if gh issue list -R "$REPO" --state all --search "in:title \"$title\"" --json number | jq -e '.[0]'; then
    echo "✅ Issue already exists: $title"; continue;
  fi

  body=$(echo "$row" | jq -r '.body // empty')
  labels=$(echo "$row" | jq -r '.labels | join(",")')
  milestone=$(echo "$row" | jq -r '.milestone // empty')
  assignees=$(echo "$row" | jq -r '.assignees | join(",")')

  issue_url=$(gh issue create -R "$REPO" \
    --title "$title" \
    ${body:+--body "$body"} \
    ${labels:+--label "$labels"} \
    ${milestone:+--milestone "$milestone"} \
    ${assignees:+--assignee "$assignees"})

  echo " Created: $title ($issue_url)"

  # Add the new issue to the project board
  gh project item-create --project "https://github.com/orgs/flexinfer/projects/1" --add-url "$issue_url"
  echo "   └── Added to project board."
done
