package root

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// boolHelpValue hides IsBoolFlag so pflag omits "[=true|false]" in help.
// Remove this workaround if https://github.com/spf13/pflag/issues/454 is fixed.
type boolHelpValue struct {
	pflag.Value
}

func flagsForHelp(flags *pflag.FlagSet) *pflag.FlagSet {
	clones := pflag.NewFlagSet("", pflag.ContinueOnError)
	clones.SortFlags = flags.SortFlags
	flags.VisitAll(func(flag *pflag.Flag) {
		clone := *flag
		if flag.Value.Type() == "bool" {
			clone.Value = boolHelpValue{flag.Value}
		}
		clones.AddFlag(&clone)
	})
	return clones
}

// FlagUsages renders flags without boolean value syntax, leaving flags unchanged.
func FlagUsages(flags *pflag.FlagSet) string {
	return flagsForHelp(flags).FlagUsages()
}

// CommandForDocs copies the command tree with compact boolean flag help.
// The copy is only for documentation generation, not command execution.
func CommandForDocs(cmd *cobra.Command) *cobra.Command {
	clone := *cmd
	clone.ResetCommands()
	clone.ResetFlags()
	clone.Flags().AddFlagSet(flagsForHelp(cmd.LocalNonPersistentFlags()))
	clone.PersistentFlags().AddFlagSet(flagsForHelp(cmd.PersistentFlags()))
	clone.InitDefaultHelpFlag()
	help := clone.Flags().Lookup("help")
	if _, wrapped := help.Value.(boolHelpValue); !wrapped && help.Value.Type() == "bool" {
		help.Value = boolHelpValue{help.Value}
	}
	for _, child := range cmd.Commands() {
		clone.AddCommand(CommandForDocs(child))
	}
	return &clone
}
