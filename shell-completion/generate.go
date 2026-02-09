//go:build generate

// TiCS: disabled // This is a helper file to generate the shell completion code.

//go:generate sh -c "go run ../cmd/authctl/main.go completion bash > bash/authctl"
//go:generate sh -c "go run ../cmd/authctl/main.go completion zsh > zsh/_authctl"
//go:generate sh -c "go run ../cmd/authctl/main.go completion fish > fish/authctl.fish"

package shell_completion
