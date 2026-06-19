// Package osrelease parse /etc/os-release
// More about the os-release: https://www.linux.org/docs/man5/os-release.html
package osrelease

import (
	"fmt"
	"os"
	"strings"
)

// Path contains the default path to the os-release file
var Path = "/etc/os-release"

var Release OSRelease

type OSRelease struct {
	Name string
	Version string
	ID string
	IDLike string
	PrettyName string
	VersionID string
	HomeURL string
	DocumentationURL string
	SupportURL string
	BugReportURL string
	PrivacyPolicyURL string
	VersionCodename string
	UbuntuCodename string
	ANSIColor string
	CPEName string
	BuildID string
	Variant string
	VariantID string
	Logo string
}

// getLines read the OSReleasePath and return it line by line.
// Empty lines and comments (beginning with a "#") are ignored.
func getLines() ([]string, error) {

	output, err := os.ReadFile(Path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %s", Path, err)
	}

	lines := make([]string, 0)

	for _, line := range strings.Split(string(output), "\n") {

		switch true {
		case line == "":
			continue
		case []byte(line)[0] == '#':
			continue
		}

		lines = append(lines, line)
	}

	return lines, nil
}

// parseLine parse a single line.
// Return key, value, error (if any)
func parseLine(line string) (string, string, error) {

	subs := strings.SplitN(line, "=", 2)

	if len(subs) != 2 {
		return "", "", fmt.Errorf("invalid length of the substrings: %d", len(subs))
	}

	return subs[0], strings.Trim(subs[1], "\"'"), nil
}

// Parse parses the os-release file pointing to by Path.
// The fields are saved into the Release global variable.
func Parse() error {

	lines, err := getLines()
	if err != nil {
		return fmt.Errorf("failed to get lines of %s: %s", Path, err)
	}

	for i := range lines {

		key, value, err := parseLine(lines[i])
		if err != nil {
			return fmt.Errorf("failed to parse line '%s': %s", lines[i], err)
		}

		switch key {
		case "NAME":
			Release.Name = value
		case "VERSION":
			Release.Version = value
		case "ID":
			Release.ID = value
		case "ID_LIKE":
			Release.IDLike = value
		case "PRETTY_NAME":
			Release.PrettyName = value
		case "VERSION_ID":
			Release.VersionID = value
		case "HOME_URL":
			Release.HomeURL = value
		case "DOCUMENTATION_URL":
			Release.DocumentationURL = value
		case "SUPPORT_URL":
			Release.SupportURL = value
		case "BUG_REPORT_URL":
			Release.BugReportURL = value
		case "PRIVACY_POLICY_URL":
			Release.PrivacyPolicyURL = value
		case "VERSION_CODENAME":
			Release.VersionCodename = value
		case "UBUNTU_CODENAME":
			Release.UbuntuCodename = value
		case "ANSI_COLOR":
			Release.ANSIColor = value
		case "CPE_NAME":
			Release.CPEName = value
		case "BUILD_ID":
			Release.BuildID = value
		case "VARIANT":
			Release.Variant = value
		case "VARIANT_ID":
			Release.VariantID = value
		case "LOGO":
			Release.Logo = value
		default:
			return fmt.Errorf("unknown key found: %s", key)
		}
	}

	return nil
}