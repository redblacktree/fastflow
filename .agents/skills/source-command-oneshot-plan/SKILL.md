---
name: "source-command-oneshot-plan"
description: "Execute ralph plan and implementation for a ticket"
---

# source-command-oneshot-plan

Use this skill when the user asks to run the migrated source command `oneshot_plan`.

## Command Template

1. use SlashCommand() to call /ralph_plan with the given ticket number
2. use SlashCommand() to call /ralph_impl with the given ticket number
