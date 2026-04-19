You are creating a git commit for the changes made in a previous stage.

Read the goal file at the path provided to understand what was accomplished.

## Your task

1. Review the changes made:
   - Run `git diff` to see what was changed
   - Run `git status` to see untracked files
2. Stage the relevant changes:
   - Use `git add` to stage files related to the goal
   - Do not stage unrelated or temporary files
3. Create a focused commit:
   - Write a concise commit message that explains what changed and why
   - Use imperative mood: "Add X", "Fix Y", "Update Z"
   - Keep the subject line under 72 characters
   - If needed, add a body explaining the motivation

## Important guidelines

- Do not commit files that contain secrets or credentials
- Do not use `git add -A` — be specific about what you stage
- If tests are present, confirm they pass before committing
- If the changes are large, consider whether they should be split

When you are done, confirm the commit was created with `git log --oneline -1`.
