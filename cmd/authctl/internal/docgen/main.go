// Package main generates CLI reference documentation.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/canonical/authd/cmd/authctl/root"
	"github.com/spf13/cobra/doc"
)

func main() {
	out := flag.String("out", "./docs/cli", "output directory")
	format := flag.String("format", "markdown", "markdown|man|rest")
	front := flag.Bool("frontmatter", false, "prepend simple YAML front matter to markdown")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o750); err != nil {
		log.Fatal(err)
	}

	rootCmd := root.RootCmd
	rootCmd.DisableAutoGenTag = true // stable, reproducible files (no timestamp footer)

	switch *format {
	case "markdown":
		if *front {
			prep := func(filename string) string {
				base := filepath.Base(filename)
				name := strings.TrimSuffix(base, filepath.Ext(base))
				title := strings.ReplaceAll(name, "_", " ")
				return fmt.Sprintf("---\ntitle: %q\nslug: %q\ndescription: \"CLI reference for %s\"\n---\n\n", title, name, title)
			}
			link := func(name string) string { return strings.ToLower(name) }
			if err := doc.GenMarkdownTreeCustom(rootCmd, *out, prep, link); err != nil {
				log.Fatal(err)
			}
		} else {
			if err := doc.GenMarkdownTree(rootCmd, *out); err != nil {
				log.Fatal(err)
			}
		}
	case "man":
		hdr := &doc.GenManHeader{Title: strings.ToUpper(rootCmd.Name()), Section: "1"}
		if err := doc.GenManTree(rootCmd, hdr, *out); err != nil {
			log.Fatal(err)
		}
	case "rest":
		if err := doc.GenReSTTree(rootCmd, *out); err != nil {
			log.Fatal(err)
		}
	default:
		log.Fatalf("unknown format: %s", *format)
	}
}
