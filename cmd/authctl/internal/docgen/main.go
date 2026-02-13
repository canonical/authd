// Package main generates CLI reference documentation.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/authd/cmd/authctl/root"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
	"github.com/spf13/pflag"
)

func logf(format string, v ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", v...)
}

func fatalf(format string, v ...any) {
	logf(format, v...)
	os.Exit(1)
}

func fatal(v ...any) {
	fatalf("%v", v...)
}

func main() {
	out := flag.String("out", "", "output path (directory for markdown/rest, file for man)")
	format := flag.String("format", "markdown", "markdown|man|rest")
	front := flag.Bool("frontmatter", false, "prepend simple YAML front matter to markdown")
	flag.Parse()

	if *out == "" {
		fatal("-out is required")
	}

	rootCmd := root.RootCmd
	rootCmd.DisableAutoGenTag = true // stable, reproducible files (no timestamp footer)

	logf("generating %s documentation in %s", *format, *out)

	switch *format {
	case "markdown":
		if err := os.MkdirAll(*out, 0o750); err != nil {
			fatal(err)
		}

		if *front {
			prep := func(filename string) string {
				base := filepath.Base(filename)
				name := strings.TrimSuffix(base, filepath.Ext(base))
				title := strings.ReplaceAll(name, "_", " ")
				return fmt.Sprintf("---\ntitle: %q\nslug: %q\ndescription: \"CLI reference for %s\"\n---\n\n", title, name, title)
			}
			link := func(name string) string { return strings.ToLower(name) }
			if err := doc.GenMarkdownTreeCustom(rootCmd, *out, prep, link); err != nil {
				fatal(err)
			}
		} else {
			if err := doc.GenMarkdownTree(rootCmd, *out); err != nil {
				fatal(err)
			}
		}
	case "rest":
		if err := os.MkdirAll(*out, 0o750); err != nil {
			fatal(err)
		}
		if err := doc.GenReSTTree(rootCmd, *out); err != nil {
			fatal(err)
		}
	case "man":
		if err := genManPage(rootCmd, *out); err != nil {
			fatal(err)
		}
	default:
		fatalf("unknown format: %s", *format)
	}
}

// genManPage generates a single man page for authctl and all its subcommands.
// We can't use cobra's doc.GenManTree because it generates separate man
// pages for each command, and we want a single page with all commands.
func genManPage(cmd *cobra.Command, path string) error {
	cmd.InitDefaultHelpFlag()
	cmd.InitDefaultVersionFlag()

	header := &doc.GenManHeader{Title: strings.ToUpper(cmd.Name()), Section: "1"}
	fillHeader(header, cmd.CommandPath())

	buf := new(bytes.Buffer)

	// Header
	fmt.Fprintf(buf, ".\\\" Generated from authctl man page generator\n")
	fmt.Fprintf(buf, ".\\\" Do not edit manually\n")
	fmt.Fprintf(buf, ".nh\n")
	fmt.Fprintf(buf, ".TH \"%s\" \"%s\" \"%s\" \"%s\" \"%s\"\n",
		header.Title, header.Section, header.Date.Format("Jan 2006"), header.Source, header.Manual)

	// NAME
	fmt.Fprintf(buf, ".SH NAME\n")
	fmt.Fprintf(buf, "%s \\- %s\n", cmd.Name(), escapeRoff(cmd.Short))

	// SYNOPSIS
	fmt.Fprintf(buf, ".SH SYNOPSIS\n")
	fmt.Fprintf(buf, ".HP\n")
	fmt.Fprintf(buf, "\\fB%s\\fP [\\fIoptions\\fP] \\fI<command>\\fP [\\fIargs\\fP]\n", cmd.Name())

	// DESCRIPTION
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	desc = escapeRoff(desc)
	// Make occurrences of the command name bold in the description
	desc = strings.ReplaceAll(desc, cmd.Name(), "\\fB"+cmd.Name()+"\\fP")
	fmt.Fprintf(buf, ".SH DESCRIPTION\n")
	fmt.Fprintf(buf, "%s\n", desc)

	// COMMANDS
	fmt.Fprintf(buf, ".SH COMMANDS\n")
	genCommandList(buf, cmd)

	// OPTIONS
	globalFlags := cmd.PersistentFlags()
	if globalFlags.HasAvailableFlags() {
		fmt.Fprintf(buf, ".SH OPTIONS\n")
		fmt.Fprintf(buf, "The following options are understood:\n")
		manPrintFlags(buf, globalFlags)
	}

	// SEE ALSO
	fmt.Fprintf(buf, ".SH SEE ALSO\n")
	fmt.Fprintf(buf, "For more information, please refer to the \\m[blue]\\fBauthd documentation\\fP\\m[][1]\\&.\n")

	// NOTES
	fmt.Fprintf(buf, ".SH NOTES\n")
	fmt.Fprintf(buf, ".IP \" 1.\" 4\n")
	fmt.Fprintf(buf, "authd documentation\n")
	fmt.Fprintf(buf, ".RS 4\n")
	fmt.Fprintf(buf, "\\%%https://documentation.ubuntu.com/authd\n")
	fmt.Fprintf(buf, ".RE\n")

	return os.WriteFile(path, buf.Bytes(), 0600)
}

func genCommandList(buf *bytes.Buffer, cmd *cobra.Command) {
	var commands []*cobra.Command
	collectCommands(cmd, &commands)

	for _, c := range commands {
		// Calculate command name relative to root
		// e.g. "user lock"
		name := c.UseLine()
		rootName := c.Root().Name()
		if strings.HasPrefix(name, rootName+" ") {
			// +1 for space
			name = name[len(rootName)+1:]
		}

		// Split command and arguments
		// Format: "command <arg1> <arg2>" -> "\fBcommand\fP \fI<arg1>\fP \fI<arg2>\fP"
		parts := strings.Fields(name)
		var formattedParts []string

		for _, part := range parts {
			if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
				// This is an argument - make it italic, keep angle brackets and lowercase
				formattedParts = append(formattedParts, "\\fI"+part+"\\fP")
			} else {
				// This is part of the command - make it bold
				formattedParts = append(formattedParts, "\\fB"+part+"\\fP")
			}
		}

		formattedName := strings.Join(formattedParts, " ")

		// Write command with proper roff formatting
		fmt.Fprintf(buf, ".PP\n")
		fmt.Fprintf(buf, "%s\n", formattedName)
		fmt.Fprintf(buf, ".RS 4\n")

		// Write description
		desc := ""
		if c.Long != "" {
			desc = c.Long
		} else if c.Short != "" {
			desc = c.Short
		}

		if desc != "" {
			// Escape special characters in description
			desc = escapeRoff(desc)
			// Write paragraphs
			paragraphs := strings.Split(desc, "\n\n")
			for i, para := range paragraphs {
				para = strings.TrimSpace(para)
				if para == "" {
					continue
				}
				// Replace newlines within paragraph with spaces
				para = strings.ReplaceAll(para, "\n", " ")
				fmt.Fprintf(buf, "%s\n", para)
				if i < len(paragraphs)-1 {
					fmt.Fprintf(buf, ".sp\n") // Add spacing between paragraphs
				}
			}
		}

		// Options
		flags := c.NonInheritedFlags()
		if flags.HasAvailableFlags() {
			fmt.Fprintf(buf, ".sp\n")
			fmt.Fprintf(buf, "\\fBOptions:\\fP\n")
			fmt.Fprintf(buf, ".sp\n")
			manPrintFlags(buf, flags)
		}

		// .RE ends indented block
		fmt.Fprintf(buf, ".RE\n")
	}
}
func collectCommands(cmd *cobra.Command, res *[]*cobra.Command) {
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if len(c.Commands()) > 0 {
			collectCommands(c, res)
		} else {
			*res = append(*res, c)
		}
	}
}

func fillHeader(header *doc.GenManHeader, name string) {
	if header.Title == "" {
		header.Title = strings.ToUpper(strings.ReplaceAll(name, " ", "\\-"))
	}
	if header.Section == "" {
		header.Section = "1"
	}
	if header.Source == "" {
		header.Source = "authd"
	}
	if header.Manual == "" {
		header.Manual = "authd"
	}
	if header.Date == nil {
		now := time.Now()
		if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
			unixEpoch, err := strconv.ParseInt(epoch, 10, 64)
			if err == nil {
				now = time.Unix(unixEpoch, 0)
			}
		}
		header.Date = &now
	}
}

func escapeRoff(s string) string {
	// Escape backslashes - this is the main special character that needs escaping
	s = strings.ReplaceAll(s, "\\", "\\\\")
	return s
}

func manPrintFlags(buf *bytes.Buffer, flags *pflag.FlagSet) {
	flags.VisitAll(func(flag *pflag.Flag) {
		if len(flag.Deprecated) > 0 || flag.Hidden {
			return
		}

		// Build flag name
		fmt.Fprintf(buf, ".PP\n")

		var flagStr string
		if len(flag.Shorthand) > 0 && len(flag.ShorthandDeprecated) == 0 {
			flagStr = fmt.Sprintf("\\fB\\-%s\\fP, \\fB\\-\\-%s\\fP", flag.Shorthand, flag.Name)
		} else {
			flagStr = fmt.Sprintf("\\fB\\-\\-%s\\fP", flag.Name)
		}

		// Add value specification for non-boolean flags
		if flag.Value.Type() != "bool" {
			if len(flag.NoOptDefVal) > 0 {
				flagStr += " ["
			} else {
				flagStr += " "
			}

			// Format value based on type
			valName := strings.ToUpper(flag.Name)
			if flag.Value.Type() == "string" {
				flagStr += fmt.Sprintf("\\fI%s\\fP", valName)
			} else {
				flagStr += fmt.Sprintf("\\fI%s\\fP", valName)
			}

			if len(flag.NoOptDefVal) > 0 {
				flagStr += "]"
			}
		}

		fmt.Fprintf(buf, "%s\n", flagStr)
		fmt.Fprintf(buf, ".RS 4\n")
		fmt.Fprintf(buf, "%s\n", escapeRoff(flag.Usage))

		// Show default value if not empty and not boolean
		if flag.Value.Type() != "bool" && flag.DefValue != "" {
			fmt.Fprintf(buf, ".sp\n")
			fmt.Fprintf(buf, "Defaults to \\fI%s\\fP\\&.\n", escapeRoff(flag.DefValue))
		}

		fmt.Fprintf(buf, ".RE\n")
	})
}
