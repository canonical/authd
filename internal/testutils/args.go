package testutils

import (
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"
)

var (
	isAsan              bool
	isAsanOnce          sync.Once
	isRace              bool
	isRaceOnce          sync.Once
	sleepMultiplier     float64
	sleepMultiplierOnce sync.Once
	testVerbosity       int
	testVerbosityOnce   sync.Once
)

// TestVerbosity returns the verbosity level that should be used in tests.
func TestVerbosity() int {
	testVerbosityOnce.Do(func() {
		if v := os.Getenv("AUTHD_TEST_VERBOSITY"); v != "" {
			var err error
			testVerbosity, err = strconv.Atoi(v)
			if err != nil {
				panic(err)
			}
		}
	})
	return testVerbosity
}

func haveBuildFlag(flag string) bool {
	b, ok := debug.ReadBuildInfo()
	if !ok {
		panic("could not read build info")
	}

	flag = "-" + flag
	for _, s := range b.Settings {
		if s.Key != flag {
			continue
		}
		value, err := strconv.ParseBool(s.Value)
		if err != nil {
			panic(err)
		}
		return value
	}

	return false
}

// IsAsan returns whether the tests are running with address sanitizer.
func IsAsan() bool {
	isAsanOnce.Do(func() { isAsan = haveBuildFlag("asan") })
	return isAsan
}

// IsRace returns whether the tests are running with thread sanitizer.
func IsRace() bool {
	isRaceOnce.Do(func() { isRace = haveBuildFlag("race") })
	return isRace
}

// GoBuildFlags returns the Go build flags that should be used when building binaries in tests.
// It includes flags for coverage, address sanitizer, and race detection if they are enabled
// in the current test environment.
//
// Note: The flags returned by this function must be the first arguments to the `go build` command,
// because -cover is a "positional flag".
func GoBuildFlags() []string {
	var flags []string
	if CoverDirForTests() != "" {
		flags = append(flags, "-cover")
	}
	if IsAsan() {
		flags = append(flags, "-asan")
	}
	if IsRace() {
		flags = append(flags, "-race")
	}
	return flags
}

// SleepMultiplier returns the sleep multiplier to be used in tests.
func SleepMultiplier() float64 {
	sleepMultiplierOnce.Do(func() {
		sleepMultiplier = 1
		if v := os.Getenv("AUTHD_TESTS_SLEEP_MULTIPLIER"); v != "" {
			var err error
			sleepMultiplier, err = strconv.ParseFloat(v, 64)
			if err != nil {
				panic(err)
			}
			if sleepMultiplier <= 0 {
				panic("Negative or 0 sleep multiplier is not supported")
			}
			return
		}

		if IsAsan() {
			sleepMultiplier *= 1.5
		}
		if IsRace() {
			sleepMultiplier *= 4
		}
	})

	return sleepMultiplier
}

// MultipliedSleepDuration returns a duration multiplied by the sleep multiplier
// provided by [MultipliedSleepDuration].
func MultipliedSleepDuration(in time.Duration) time.Duration {
	return time.Duration(math.Round(float64(in) * SleepMultiplier()))
}

// IsCI returns whether the test is running in CI environment.
var IsCI = sync.OnceValue(func() bool {
	_, ok := os.LookupEnv("GITHUB_ACTIONS")
	return ok
})

// IsDebianPackageBuild returns true if the tests are running in a Debian package build environment.
var IsDebianPackageBuild = sync.OnceValue(func() bool {
	_, ok := os.LookupEnv("DEB_BUILD_ARCH")
	return ok
})
