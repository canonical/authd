// Package semver implements comparison of semantic version strings.
package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/mod/semver"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "check":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: 'check' requires exactly one version argument\n")
			usage()
			os.Exit(1)
		}
		checkVersion(os.Args[2])

	case "compare":
		if len(os.Args) != 4 {
			fmt.Fprintf(os.Stderr, "Error: 'compare' requires exactly two version arguments\n")
			usage()
			os.Exit(1)
		}
		compareVersions(os.Args[2], os.Args[3])

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: %[1]s <command> [arguments]

Commands:
  check <version>           Check if a version is valid
  compare <ver1> <ver2>     Compare two versions

Examples:
  %[1]s check 1.2.3           # Prints "valid" or "invalid"
  %[1]s compare 1.2.3 2.0.0   # Prints "less", "equal", or "greater"
`, os.Args[0])
}

func addVPrefix(version string) string {
	if !strings.HasPrefix(version, "v") {
		return "v" + version
	}
	return version
}

func checkVersion(version string) {
	v := addVPrefix(version)
	if semver.IsValid(v) {
		fmt.Println("valid")
		return
	}
	fmt.Println("invalid")
	os.Exit(1)
}

func compareVersions(ver1, ver2 string) {
	v1 := addVPrefix(ver1)
	v2 := addVPrefix(ver2)

	if !semver.IsValid(v1) || !semver.IsValid(v2) {
		fmt.Fprintf(os.Stderr, "Error: invalid semantic version format\n")
		os.Exit(1)
	}

	switch semver.Compare(v1, v2) {
	case -1:
		fmt.Println("less")
	case 0:
		fmt.Println("equal")
	case 1:
		fmt.Println("greater")
	}
}
