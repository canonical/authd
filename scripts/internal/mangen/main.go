// Package main writes the man page for apply-entra-unix-ids from the --help
// text of the script, so the wording lives in one place.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const (
	usageLabel       = "Usage:"
	environmentLabel = "Environment:"
	exitStatusLabel  = "Exit status:"

	// seeAlso is fixed because the script runs authctl, which has its own man page.
	seeAlso = `\fBauthctl\fP(1)`
)

// placeholderPattern matches placeholder names in the usage line, such as NAME.
var placeholderPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// helpSections holds the parts of the help text that become man page sections.
type helpSections struct {
	name        string
	summary     string
	usage       string
	description []string
	environment string
	exitStatus  string
}

// labelledSection links a labelled paragraph in the help text to its field.
type labelledSection struct {
	label  string
	target *string
}

func main() {
	log.SetFlags(0)

	script := flag.String("script", "", "path to the apply-entra-unix-ids script")
	out := flag.String("out", "", "path of the man page to write")
	flag.Parse()

	if *script == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*script, *out); err != nil {
		log.Fatalf("Failed to generate man page: %v", err)
	}
}

func run(scriptPath, outPath string) error {
	help, err := scriptHelp(scriptPath)
	if err != nil {
		return err
	}

	page, err := render(help)
	if err != nil {
		return err
	}

	return os.WriteFile(outPath, []byte(page), 0o600)
}

// scriptHelp returns the text that the script prints for --help.
func scriptHelp(scriptPath string) (string, error) {
	var stdout, stderr bytes.Buffer
	//nolint:gosec // The script path comes from the go:generate directive, not from user input.
	cmd := exec.Command("bash", scriptPath, "--help")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running %s --help: %w: %s", scriptPath, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// render builds the man page from the help text.
func render(help string) (string, error) {
	sections, err := parseHelp(help)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, ".\\\" Generated from the --help text of %s\n", sections.name)
	b.WriteString(".\\\" Do not edit manually\n")
	b.WriteString(".nh\n")
	fmt.Fprintf(&b, ".TH \"%s\" \"1\" \"\" \"authd\"\n", strings.ToUpper(sections.name))

	b.WriteString(".SH NAME\n")
	fmt.Fprintf(&b, "%s \\- %s\n", roffEscape(sections.name), roffEscape(sections.summary))

	b.WriteString(".SH SYNOPSIS\n")
	fmt.Fprintf(&b, "%s\n", renderSynopsis(sections.usage))

	b.WriteString(".SH DESCRIPTION\n")
	for i, paragraph := range sections.description {
		if i > 0 {
			b.WriteString(".PP\n")
		}
		fmt.Fprintf(&b, "%s\n", roffText(paragraph))
	}

	b.WriteString(".SH ENVIRONMENT\n")
	fmt.Fprintf(&b, "%s\n", roffText(sections.environment))

	b.WriteString(".SH \"EXIT STATUS\"\n")
	fmt.Fprintf(&b, "%s\n", roffText(sections.exitStatus))

	b.WriteString(".SH \"SEE ALSO\"\n")
	fmt.Fprintf(&b, "%s\n", seeAlso)

	return b.String(), nil
}

// parseHelp splits the help text into man page sections. The first paragraph
// is the summary, the labelled paragraphs fill the usage, environment, and
// exit status sections, and all other paragraphs become the description.
func parseHelp(help string) (helpSections, error) {
	paragraphs := splitParagraphs(help)
	if len(paragraphs) == 0 {
		return helpSections{}, errors.New("help text is empty")
	}

	name, summary, found := strings.Cut(paragraphs[0], " - ")
	if !found || strings.Contains(paragraphs[0], "\n") ||
		name == "" || strings.ContainsAny(name, " \t") || summary == "" {
		return helpSections{}, errors.New(`first paragraph must be a one-line summary of the form "name - description"`)
	}

	s := helpSections{name: name, summary: summary}
	sections := []labelledSection{
		{usageLabel, &s.usage},
		{environmentLabel, &s.environment},
		{exitStatusLabel, &s.exitStatus},
	}
	seen := make(map[string]bool, len(sections))

	for _, paragraph := range paragraphs[1:] {
		text := strings.Join(strings.Fields(paragraph), " ")
		matched := false
		for _, sec := range sections {
			if !strings.HasPrefix(text, sec.label) {
				continue
			}
			if seen[sec.label] {
				return helpSections{}, fmt.Errorf("help text has more than one %q paragraph", sec.label)
			}
			seen[sec.label] = true
			*sec.target = strings.TrimSpace(strings.TrimPrefix(text, sec.label))
			matched = true
			break
		}
		if !matched {
			s.description = append(s.description, text)
		}
	}

	for _, sec := range sections {
		if !seen[sec.label] {
			return helpSections{}, fmt.Errorf("help text has no %q paragraph", sec.label)
		}
	}

	if fields := strings.Fields(s.usage); len(fields) == 0 || fields[0] != name {
		return helpSections{}, fmt.Errorf("the %q paragraph must start with the command name %q", usageLabel, name)
	}

	return s, nil
}

// splitParagraphs splits the text into blocks of lines separated by blank lines.
func splitParagraphs(text string) []string {
	var paragraphs []string
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			if len(lines) > 0 {
				paragraphs = append(paragraphs, strings.Join(lines, "\n"))
				lines = nil
			}
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		paragraphs = append(paragraphs, strings.Join(lines, "\n"))
	}
	return paragraphs
}

// renderSynopsis formats the usage line. The command name is bold and the
// placeholders are italic.
func renderSynopsis(usage string) string {
	fields := strings.Fields(usage)
	parts := make([]string, 0, len(fields))
	for i, field := range fields {
		if i == 0 {
			parts = append(parts, `\fB`+roffEscape(field)+`\fP`)
			continue
		}
		parts = append(parts, renderSynopsisToken(field))
	}
	return strings.Join(parts, " ")
}

// renderSynopsisToken formats one word of the usage line. Brackets stay
// outside the formatting, so "[--apply]" keeps its brackets.
func renderSynopsisToken(token string) string {
	withoutOpen := strings.TrimLeft(token, "[")
	open := token[:len(token)-len(withoutOpen)]
	core := strings.TrimRight(withoutOpen, "]")
	suffix := withoutOpen[len(core):]
	if placeholderPattern.MatchString(core) {
		return open + `\fI` + core + `\fP` + suffix
	}
	return open + roffEscape(core) + suffix
}

// roffText escapes plain text for roff. A line that starts with a dot or a
// quote would be read as a request, so it gets a zero-width escape first.
func roffText(s string) string {
	s = roffEscape(s)
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "'") {
		s = `\&` + s
	}
	return s
}

// roffEscape escapes backslashes and hyphens, so that they print as typed.
func roffEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\e`)
	return strings.ReplaceAll(s, "-", `\-`)
}
