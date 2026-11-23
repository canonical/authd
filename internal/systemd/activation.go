// Package systemd handles authd's socket activation and descriptor store.
package systemd

import (
	"net"
	"os"
	"strings"
	"sync"

	"github.com/coreos/go-systemd/v22/activation"
)

// StorePrefix distinguishes reservation descriptors from listening sockets.
const StorePrefix = "pa-"

var activationFiles = sync.OnceValue(activation.FilesWithNames)

// Listeners returns only listening sockets, not reservation descriptors.
func Listeners() ([]net.Listener, error) {
	return listenersFromFiles(activationFiles())
}

func listenersFromFiles(filesByName map[string][]*os.File) ([]net.Listener, error) {
	var listeners []net.Listener
	for name, files := range filesByName {
		if strings.HasPrefix(name, StorePrefix) {
			continue
		}
		for _, file := range files {
			listener, err := net.FileListener(file)
			file.Close()
			if err != nil {
				return nil, err
			}
			listeners = append(listeners, listener)
		}
	}
	return listeners, nil
}

func storedFiles() map[string][]*os.File {
	files := make(map[string][]*os.File)
	for name, descriptors := range activationFiles() {
		if strings.HasPrefix(name, StorePrefix) {
			files[name] = descriptors
		}
	}
	return files
}
