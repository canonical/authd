// Package main generates CLI reference documentation.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/authd/cmd/authctl/root"
	"github.com/spf13/cobra/doc"
)

func logf(format string, v ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", v...)
}

func log(v ...any) {
	logf("%v", v...)
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

	switch *format {
	case "markdown":
		logf("Generating markdown documentation in %s", *out)
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
		logf("Generating reStructuredText documentation in %s", *out)
		if err := os.MkdirAll(*out, 0o750); err != nil {
			fatal(err)
		}
		if err := doc.GenReSTTree(rootCmd, *out); err != nil {
			fatal(err)
		}
	case "man":
		logf("Generating man page in %s", *out)
		if err := genManPage(rootCmd, *out); err != nil {
			fatal(err)
		}
	default:
		fatalf("unknown format: %s", *format)
	}
}
