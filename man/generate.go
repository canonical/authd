//go:build generate

// TiCS: disabled // This is a helper file to generate the CLI documentation

//go:generate sh -c "go run ../cmd/authctl/internal/docgen -format man -out authctl.1"
//go:generate sh -c "go run ../scripts/internal/mangen -script ../scripts/authd-entra-unix-ids -out authd-entra-unix-ids.1"

package docs
