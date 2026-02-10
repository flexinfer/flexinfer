# FlexInfer Governance

This document describes the governance model for the FlexInfer project.

## Maintainers

Maintainers are responsible for the overall direction, code review, and release process.

| Name | GitHub | Role |
|------|--------|------|
| TBD | @tbd | Lead Maintainer |

## Decision Making

- **Consensus**: Decisions are made by consensus among maintainers.
- **Lazy consensus**: Proposals are accepted if no maintainer objects within 72 hours.
- **Voting**: When consensus cannot be reached, a simple majority vote among maintainers decides.
- **Tie-breaking**: The lead maintainer breaks ties.

## Becoming a Maintainer

New maintainers are nominated by existing maintainers and accepted by lazy consensus.

Requirements:
- Sustained contributions over 3+ months
- Demonstrated understanding of project architecture
- Track record of quality code reviews
- Alignment with project goals and values

## Code Review

- All changes require at least one maintainer approval
- Security-sensitive changes require two maintainer approvals
- Maintainers should not merge their own PRs without review

## Releases

- Releases follow semantic versioning (semver)
- Release candidates are tagged for testing before stable releases
- Breaking changes require a deprecation period of at least one minor release

## Meetings

- Community meetings are held monthly (schedule TBD)
- Meeting notes are published in the `docs/meetings/` directory

## Changes to Governance

Changes to this document require approval from a majority of maintainers.
