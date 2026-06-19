package errmessages

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRedactErrorInterceptor(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		inputError error

		wantMessage string
	}{
		"Trim_input_down_to_ErrToDisplay": {
			inputError:  fmt.Errorf("Error to be redacted: %w", ToDisplayError{errors.New("Error to be shown")}),
			wantMessage: "Error to be shown",
		},
		"Return_original_error": {
			inputError:  errors.New("Not a redacted error"),
			wantMessage: "Not a redacted error",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := RedactErrorInterceptor(context.TODO(), testRequest{tc.inputError}, nil, testHandler)
			require.Error(t, err, "RedactErrorInterceptor should return an error")
			require.Equal(t, tc.wantMessage, err.Error(), "RedactErrorInterceptor returned unexpected error message")
		})
	}
}

func TestFormatErrorMessage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		inputError error

		wantMessage string
	}{
		"Non-gRPC_error_is_left_untouched": {
			inputError:  errors.New("Non-gRPC error"),
			wantMessage: "Non-gRPC error",
		},
		"Unrecognized_error_is_left_untouched": {
			inputError:  status.Error(100, "Unrecognized error"),
			wantMessage: "error Code(100) from server: Unrecognized error",
		},
		"Code_Canceled_is_left_untouched": {
			inputError:  status.Error(codes.Canceled, "Canceled error"),
			wantMessage: "rpc error: code = Canceled desc = Canceled error",
		},

		"Parse_code_Unavailable": {
			inputError:  status.Error(codes.Unavailable, "Unavailable error"),
			wantMessage: "couldn't connect to authd daemon: Unavailable error",
		},
		"Parse_code_DeadlineExceeded": {
			inputError:  status.Error(codes.DeadlineExceeded, "DeadlineExceeded error"),
			wantMessage: "service took too long to respond. Disconnecting client",
		},
		"Parse_code_Unknown": {
			inputError:  status.Error(codes.Unknown, "Unknown error"),
			wantMessage: "Unknown error",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := FormatErrorMessage(context.TODO(), "", testRequest{tc.inputError}, nil, nil, testInvoker)
			require.Error(t, err, "FormatErrorMessage should return an error")
			require.Equal(t, tc.wantMessage, err.Error(), "FormatErrorMessage returned unexpected error message")
		})
	}
}

type testRequest struct {
	err error
}

func testHandler(ctx context.Context, req any) (any, error) {
	//nolint:forcetypeassert // This is only used in the tests and we know the type.
	return nil, req.(testRequest).err
}

func testInvoker(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
	//nolint:forcetypeassert // This is only used in the tests and we know the type.
	return req.(testRequest).err
}
