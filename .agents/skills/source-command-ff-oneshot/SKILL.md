---
name: "source-command-ff-oneshot"
description: "Research ticket and launch planning session"
---

# source-command-ff-oneshot

Use this skill when the user asks to run the migrated source command `ff_oneshot`.

## Command Template

1. use SlashCommand() to call /ralph_research with the given ticket number
2. launch a new session with `npx humanlayer launch --model opus --dangerously-skip-permissions --dangerously-skip-permissions-timeout 14m --title "plan ENG-XXXX" "/oneshot_plan ENG-XXXX"`
