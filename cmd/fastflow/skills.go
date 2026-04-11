package main

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/redblacktree/fastflow/internal/output"
	"github.com/redblacktree/fastflow/internal/skills"
	"github.com/spf13/cobra"
)

var (
	flagSkillsList  bool
	flagSkillsForce bool
)

var skillsCmd = &cobra.Command{
	Use:   "skills",
	Short: "Manage fastflow skills",
	Long:  "Install, update, and manage fastflow Claude Code skills.",
}

var skillsInstallCmd = &cobra.Command{
	Use:   "install [name...]",
	Short: "Install fastflow skills",
	Long:  "Extract embedded skills to ~/.claude/skills/fastflow/. Optionally specify skill names to install specific skills only.",
	RunE:  runSkillsInstall,
}

var skillsUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update installed skills (alias for install --force)",
	RunE:  runSkillsUpdate,
}

var skillsPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print skills install directory",
	RunE:  runSkillsPath,
}

func init() {
	skillsInstallCmd.Flags().BoolVar(&flagSkillsList, "list", false, "List available skills")
	skillsInstallCmd.Flags().BoolVar(&flagSkillsForce, "force", false, "Overwrite existing skills")

	skillsCmd.AddCommand(skillsInstallCmd)
	skillsCmd.AddCommand(skillsUpdateCmd)
	skillsCmd.AddCommand(skillsPathCmd)
}

func runSkillsInstall(cmd *cobra.Command, args []string) error {
	success := color.New(color.FgGreen).SprintFunc()
	warning := color.New(color.FgYellow).SprintFunc()

	if flagSkillsList {
		names, err := skills.List()
		if err != nil {
			return fmt.Errorf("failed to list skills: %w", err)
		}
		output.Printf("Available skills (%d):\n", len(names))
		for _, name := range names {
			output.Printf("  %s\n", name)
		}
		return nil
	}

	created, skipped, overwritten, err := skills.Install(args, flagSkillsForce)
	if err != nil {
		return fmt.Errorf("failed to install skills: %w", err)
	}

	dir, _ := skills.InstallDir()

	if len(created) > 0 {
		output.Printf("%s Installed %d skill(s) to %s:\n", success("OK"), len(created), dir)
		for _, f := range created {
			output.Printf("  %s %s\n", color.GreenString("created"), f)
		}
	}

	if len(overwritten) > 0 {
		output.Printf("%s Updated %d skill(s):\n", success("OK"), len(overwritten))
		for _, f := range overwritten {
			output.Printf("  %s %s\n", color.YellowString("updated"), f)
		}
	}

	if len(skipped) > 0 {
		output.Printf("%s Skipped %d existing skill(s) (use --force to overwrite):\n", warning("SKIP"), len(skipped))
	}

	if len(created) == 0 && len(overwritten) == 0 && len(skipped) == 0 {
		output.Printf("No skills to install.\n")
	}

	return nil
}

func runSkillsUpdate(cmd *cobra.Command, args []string) error {
	success := color.New(color.FgGreen).SprintFunc()

	created, skipped, overwritten, err := skills.Install(nil, true)
	if err != nil {
		return fmt.Errorf("failed to update skills: %w", err)
	}

	_ = skipped // with force=true, skipped is always empty

	dir, _ := skills.InstallDir()
	total := len(created) + len(overwritten)

	if total > 0 {
		output.Printf("%s Updated %d skill(s) in %s\n", success("OK"), total, dir)
		updated := append(created, overwritten...)
		for _, f := range updated {
			output.Printf("  %s %s\n", color.GreenString("ok"), f)
		}
	} else {
		output.Printf("No skills to update.\n")
	}

	return nil
}

func runSkillsPath(cmd *cobra.Command, args []string) error {
	dir, err := skills.InstallDir()
	if err != nil {
		return fmt.Errorf("failed to get skills directory: %w", err)
	}
	output.Println(strings.TrimSpace(dir))
	return nil
}
