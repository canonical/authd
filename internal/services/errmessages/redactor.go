// Package errmessages formats the error messages that are sent to the client.
package errmessages

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RedactErrorInterceptor redacts some of the attached errors before sending it to the client.
//
// It unwraps the error up to the first ErrToDisplay and sends it to the client. If none is found, it sends the original error.
func RedactErrorInterceptor(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	m, err := handler(ctx, req)
	if err != nil {
		var redactedError ToDisplayError
		if !errors.As(err, &redactedError) {
			return m, err
		}
		return m, redactedError
	}
	return m, nil
}

// formattedError is an error with a human-readable message that still carries
// the original gRPC error, so that its status can be retrieved.
type formattedError struct {
	message string
	err     error
}

func (e formattedError) Error() string { return e.message }
func (e formattedError) Unwrap() error { return e.err }

// FormatErrorMessage formats the error message received by the client to avoid printing useless information.
//
// It converts the gRPC error to a more human-readable error with a better message,
// keeping the gRPC status available through [status.Code] and [status.FromError].
func FormatErrorMessage(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if err == nil {
		return nil
	}
	st, grpcErr := status.FromError(err)
	if !grpcErr {
		return err
	}

	var message string
	switch st.Code() {
	// no daemon
	case codes.Unavailable:
		message = fmt.Sprintf("couldn't connect to authd daemon: %v", st.Message())
	// timeout
	case codes.DeadlineExceeded:
		message = "service took too long to respond. Disconnecting client"
	// regular error without annotation
	case codes.Unknown:
		message = st.Message()
	// likely means that IsAuthenticated got cancelled, so we need to keep the error intact
	case codes.Canceled:
		return err
	case codes.PermissionDenied:
		// permission denied, just format it
		message = fmt.Sprintf("permission denied: %v", st.Message())
	// grpc error, just format it
	default:
		message = fmt.Sprintf("error %s from server: %v", st.Code(), st.Message())
	}
	return formattedError{message: message, err: err}
}
