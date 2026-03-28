package coordinator

// Prompt templates for each coordinator subsystem.
// All prompts request JSON output for reliable parsing.

const promptSessionSummarize = `You are a session summarizer for an AI coding agent system.

Given a list of context entries from an agent's session, produce a structured JSON summary.

The summary should capture:
1. What the agent worked on (high-level goals)
2. Key decisions made and their rationale
3. Important findings or discoveries
4. Files that were modified or discussed
5. Any unresolved issues or next steps

Respond with ONLY valid JSON in this exact format:
{
  "summary": "A 2-3 sentence overview of the session",
  "key_findings": ["finding 1", "finding 2"],
  "key_decisions": ["decision 1 with rationale", "decision 2 with rationale"],
  "files_touched": ["path/to/file1.go", "path/to/file2.ts"],
  "unresolved": ["issue still open"],
  "tags": ["tag1", "tag2"]
}`

const promptMemoryCompress = `You are a memory compression engine for an AI coding agent system.

Given a memory item's content, produce a compressed version that preserves:
- Decisions and their rationale
- File paths and code references
- Error messages and their solutions
- Key technical details

Remove:
- Redundant explanations
- Verbose formatting
- Repeated information

Respond with ONLY valid JSON:
{
  "compressed": "The compressed content preserving key information",
  "keywords": ["keyword1", "keyword2"],
  "importance": "critical|high|medium|low"
}`

const promptMergeSuggestions = `You are a memory deduplication engine for an AI coding agent system.

Given a list of memory items, identify groups that can be merged because they
cover the same topic, decision, or code area. Each group should be merged into
a single consolidated item.

Respond with ONLY valid JSON:
{
  "merge_groups": [
    {
      "ids": ["id1", "id2"],
      "reason": "Why these should be merged",
      "merged_title": "Title for the merged item",
      "merged_content": "Consolidated content preserving all unique information"
    }
  ],
  "skip_ids": ["id3"]
}`

const promptTriageEntries = `You are a context triage engine for an AI coding agent system.

Given a batch of context entries, classify each by importance and assign categories.

Importance levels:
- critical: Decisions that affect architecture, security issues, breaking changes
- high: Key findings, important code changes, resolved bugs
- medium: Regular progress updates, minor decisions
- low: Routine actions, navigation, boilerplate

Categories (assign 1-3): architecture, bugfix, feature, refactor, config, docs, test, security, performance, dependency, exploration

Respond with ONLY valid JSON:
{
  "results": [
    {
      "entry_id": "the-entry-id",
      "importance": "critical|high|medium|low",
      "categories": ["category1", "category2"],
      "duplicate_of": "other-entry-id-or-empty"
    }
  ]
}`

const promptEntityExtraction = `You are a knowledge graph extraction engine for an AI coding agent system.

Given context entries from an agent session, extract entities and relations
for a knowledge graph. Focus on:

Entity types: file, function, service, package, concept, decision, bug, feature
Relation types: depends_on, calls, implements, caused, resolved, modifies, references, blocks

Respond with ONLY valid JSON:
{
  "entities": [
    {
      "name": "entity name (use canonical form, e.g., full path for files)",
      "entity_type": "file|function|service|package|concept|decision|bug|feature",
      "properties": {"key": "value"}
    }
  ],
  "relations": [
    {
      "source": "source entity name",
      "target": "target entity name",
      "relation_type": "depends_on|calls|implements|caused|resolved|modifies|references|blocks"
    }
  ]
}`

const promptWorkflowPlan = `You are a workflow planner for an AI coding agent system.

Given a natural language goal description, decompose it into a directed acyclic graph
(DAG) of workflow steps. Each step should be concrete and actionable.

Step types:
- tool: An automated action (e.g., run tests, build, lint, apply code edits)
- approval: Requires human review before continuing
- gate: A conditional check that must pass

Available MCP tools you can reference in tool steps:
- server_name: "morph_fast_apply", tool_name: "edit_file" — Apply code edits to files (params: file_path, instruction, code_update)
- server_name: "morph_fast_apply", tool_name: "morph_edit_file" — Morphic code edit (params: file_path, instruction, code_update)
- server_name: "devbox", tool_name: "devbox_exec" — Run commands in sandbox (params: project, command, agent_id)
- server_name: "devbox", tool_name: "devbox_build" — Build sandbox image (params: project, agent_id)
- server_name: "agent-context", tool_name: "agent_recall" — Recall context (params: query, scope, session_id, token_budget)
- server_name: "agent-context", tool_name: "agent_session_start" — Start agent session (params: namespace, agent_id, description)
- server_name: "agent-context", tool_name: "agent_session_end" — End agent session (params: session_id, summarize)

For tool steps, include "tool_name", "server_name", and "tool_args" in the config object
so the workflow engine can dispatch them to the correct MCP server.

Respond with ONLY valid JSON:
{
  "name": "workflow-name-kebab-case",
  "description": "What this workflow accomplishes",
  "steps": [
    {
      "id": "step-1",
      "name": "Human-readable step name",
      "type": "tool|approval|gate",
      "description": "What this step does",
      "depends_on": [],
      "config": {
        "tool_name": "optional: MCP tool name",
        "server_name": "optional: MCP server name",
        "tool_args": {}
      }
    }
  ]
}`
