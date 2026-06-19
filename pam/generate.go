// TiCS: disabled // This is a helper file to generate the pam module easily.

//go:build generate && !pam_module_generation && !pam_debug

//go:generate go generate -C internal/proto

//go:generate ./generate.sh -tags !pam_debug

package main
