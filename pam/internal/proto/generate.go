// TiCS: disabled // This is a helper to compile the proto files.

//go:build generate

//go:generate ../../../tools/generate-proto.sh pam.proto

package proto
