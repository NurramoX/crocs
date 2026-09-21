package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"crocs/internal/output"
	skill "crocs/skills/crocs"

	"github.com/spf13/cobra"
)

var skillTarget string

var skillCmd = &cobra.Command{
	Use:   "install-skill",
	Short: "Write the embedded crocs skill to a skills directory",
	Long: `Write the embedded skill files to <target>/crocs/ so Claude Code (or any
agent honoring the agent-skill convention) can pick them up. Default
target is $HOME/.claude/skills/. Existing files at the target are
overwritten.

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
		entries, err := fs.ReadDir(skill.Files, ".")
		if err != nil {
			return fmt.Errorf("read embedded skill: %w", err)
		}
		resp := installSkillResponse{Path: dest}
		for _, e := range entries {
			data, err := fs.ReadFile(skill.Files, e.Name())
			if err != nil {
				return fmt.Errorf("read embedded %s: %w", e.Name(), err)
			}
			path := filepath.Join(dest, e.Name())
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			resp.Files = append(resp.Files, e.Name())
			resp.Bytes += len(data)
		}
		return output.Write(cmd.OutOrStdout(), "install-skill", resp)
	},
}

type installSkillResponse struct {
	Path  string   `json:"path"`
	Files []string `json:"files"`
	Bytes int      `json:"bytes"`
}

func init() {
	skillCmd.Flags().StringVar(&skillTarget, "target", "", "skills directory to install into (default: $HOME/.claude/skills)")
	rootCmd.AddCommand(skillCmd)
}
