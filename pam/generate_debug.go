// TiCS: disabled // This is a helper file to generate the pam module easily in debug mode.

//go:build generate && !pam_module_generation && pam_debug

//go:generate go generate -C internal/proto

//go:generate env CFLAGS=-g3 CGO_CFLAGS=-g3 ./generate.sh -tags pam_debug -build-tags pam_gdm_debug -build-flags "\"-gcflags=all=-N -l\"" -output pam_module_debug.go

package main
