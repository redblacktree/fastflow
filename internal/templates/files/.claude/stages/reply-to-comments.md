---
description: Reply to PR comments after addressing feedback
---

# Reply to Comments Stage

Read the goal file at `thoughts/shared/runs/{ticket}/goal.md` to understand the context.

Then use `/ff_reply_to_comments` to post replies to PR comments that have been addressed during implementation.

## Purpose

This stage ensures reviewers are notified when their feedback has been addressed by posting replies to their comments. This creates a clear audit trail and improves collaboration.

## Process

1. Identify the PR for the current branch
2. Fetch all comments that need replies
3. For each addressed comment, post a reply indicating:
   - What was changed
   - Where to find the changes (commit/line reference)
   - Any follow-up discussion needed

## Output

The stage should output:
- List of comments replied to
- Summary of what was addressed
- Any comments that couldn't be replied to (with reasons)