package root_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/canonical/authd/cmd/authctl/root"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/stretchr/testify/require"
)

func TestBooleanFlagHelp(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "test", Run: func(*cobra.Command, []string) {}}
	cmd.SetUsageTemplate(root.RootCmd.UsageTemplate())
	cmd.Flags().BoolP("force", "f", false, "Skip confirmation")
	cmd.Flags().Bool("verbose", true, "Show details")
	cmd.Flags().String("name", "", "Select a name")
	cmd.Flags().Bool("hidden", false, "Hidden flag")
	require.NoError(t, cmd.Flags().MarkHidden("hidden"))
	cmd.InitDefaultHelpFlag()

	forceValue := cmd.Flags().Lookup("force").Value
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	require.NoError(t, cmd.Help())
	require.Contains(t, buf.String(), "  -f, --force         Skip confirmation\n")
	require.Contains(t, buf.String(), "  -h, --help          help for test\n")
	require.Contains(t, buf.String(), "      --name string   Select a name\n")
	require.Contains(t, buf.String(), "      --verbose       Show details (default true)\n")
	require.NotContains(t, buf.String(), "--hidden")
	require.Same(t, forceValue, cmd.Flags().Lookup("force").Value)

	require.NoError(t, cmd.ParseFlags([]string{"--force", "--verbose=false"}))
	force, err := cmd.Flags().GetBool("force")
	require.NoError(t, err)
	require.True(t, force)
	verbose, err := cmd.Flags().GetBool("verbose")
	require.NoError(t, err)
	require.False(t, verbose)
}

func TestBooleanFlagDocs(t *testing.T) {
	t.Parallel()

	for name, generate := range map[string]func(*cobra.Command, io.Writer) error{
		"markdown": doc.GenMarkdown,
		"rest":     doc.GenReST,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{Use: "test"}
			cmd.PersistentFlags().Bool("verbose", false, "Show details")
			child := &cobra.Command{Use: "child", Run: func(*cobra.Command, []string) {}}
			child.Flags().BoolP("force", "f", false, "Skip confirmation")
			cmd.AddCommand(child)

			verboseValue := cmd.PersistentFlags().Lookup("verbose").Value
			forceValue := child.Flags().Lookup("force").Value
			clone := root.CommandForDocs(cmd)
			require.Same(t, verboseValue, cmd.PersistentFlags().Lookup("verbose").Value)
			require.Same(t, forceValue, child.Flags().Lookup("force").Value)
			require.Same(t, cmd, child.Parent())
			require.Nil(t, cmd.Flags().Lookup("help"))
			require.Nil(t, child.Flags().Lookup("help"))

			buf := new(bytes.Buffer)
			require.NoError(t, generate(clone.Commands()[0], buf))
			require.Contains(t, buf.String(), "  -f, --force   Skip confirmation\n")
			require.Contains(t, buf.String(), "  -h, --help    help for child\n")
			require.Contains(t, buf.String(), "      --verbose   Show details\n")
			require.NotContains(t, buf.String(), "[=true|false]")
			require.Same(t, verboseValue, cmd.PersistentFlags().Lookup("verbose").Value)
			require.Same(t, forceValue, child.Flags().Lookup("force").Value)
		})
	}
}

func TestFlagUsages(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{Use: "test"}
	flags := cmd.Flags()
	flags.SortFlags = false
	flags.BoolP("verbose", "v", true, "Show details")
	flags.Bool("force", false, "Skip confirmation")
	flags.String("name", "", "Select a name")
	value := flags.Lookup("verbose").Value

	require.Equal(t, "  -v, --verbose       Show details (default true)\n"+
		"      --force         Skip confirmation\n"+
		"      --name string   Select a name\n", root.FlagUsages(flags))
	require.Same(t, value, flags.Lookup("verbose").Value)
	require.Contains(t, flags.FlagUsages(), "--verbose[=true|false]")
}
