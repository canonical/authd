package adapter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeModelFormatInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		serviceName string
		title       string
		message     string
		want        string
	}{
		"Regular_service_with_message": {
			title:   "Authentication",
			message: "Enter a password",
			want:    "== Authentication ==\nEnter a password",
		},
		"Regular_service_without_message": {
			title: "Authentication",
			want:  "== Authentication ==",
		},
		"Polkit_service_with_message": {
			serviceName: polkitServiceName,
			title:       "Authentication",
			message:     "Enter a password",
			want:        "Enter a password",
		},
		"Polkit_service_without_message": {
			serviceName: polkitServiceName,
			title:       "Authentication",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m := nativeModel{serviceName: tc.serviceName}
			require.Equal(t, tc.want, m.formatInfo(tc.title, tc.message))
		})
	}
}
