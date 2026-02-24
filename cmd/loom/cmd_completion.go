package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd(rootCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for loom.

To load completions:

Bash:
  $ source <(loom completion bash)
  # Or add to ~/.bashrc:
  $ loom completion bash > /usr/local/etc/bash_completion.d/loom

Zsh:
  $ source <(loom completion zsh)
  # Or install permanently:
  $ loom completion zsh > "${fpath[1]}/_loom"

Fish:
  $ loom completion fish | source
  # Or install permanently:
  $ loom completion fish > ~/.config/fish/completions/loom.fish

PowerShell:
  PS> loom completion powershell | Out-String | Invoke-Expression`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return rootCmd.GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				return rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				return rootCmd.GenFishCompletion(os.Stdout, true)
			case "powershell":
				return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
}
