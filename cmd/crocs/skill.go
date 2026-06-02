package main

import (
	"fmt"
	"os"
	"path/filepath"

	"crocs/internal/output"
	"crocs/internal/skill"

	"github.com/spf13/cobra"
)

var skillTarget string

var skillCmd = &cobra.Command{
	Use:   "install-skill",
	Short: "Write the embedded crocs skill to a skills directory",
	Long: `Write the embedded SKILL.md to <target>/crocs/SKILL.md so Claude Code (or
any agent honoring the agent-skill convention) can pick it up. Default
target is $HOME/.claude/skills/.

The skill is embedded into the binary at build time — this works on a
binary obtained via "go install" with no source checkout needed.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		target := skillTarget
		if target == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("locate home dir: %w", err)
			}
			target = filepath.Join(home, ".claude", "skills")
		}
		dest := filepath.Join(target, "crocs")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dest, err)
		}
		path := filepath.Join(dest, "SKILL.md")
		if err := os.WriteFile(path, skill.Content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return output.Write(cmd.OutOrStdout(), "install-skill", installSkillResponse{
			Path:  path,
			Bytes: len(skill.Content),
		})
	},
}

type installSkillResponse struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

func init() {
	skillCmd.Flags().StringVar(&skillTarget, "target", "", "skills directory to install into (default: $HOME/.claude/skills)")
	rootCmd.AddCommand(skillCmd)
}
