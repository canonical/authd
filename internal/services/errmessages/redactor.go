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

// FormatErrorMessage formats the error message received by the client to avoid printing useless information.
//
// It converts the gRPC error to a more human-readable error with a better message.
func FormatErrorMessage(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(ctx, method, req, reply, cc, opts...)
	if err == nil {
		return nil
	}
	st, grpcErr := status.FromError(err)
	if !grpcErr {
		return err
	}

	switch st.Code() {
	// no daemon
	case codes.Unavailable:
		err = fmt.Errorf("couldn't connect to authd daemon: %v", st.Message())
	// timeout
	case codes.DeadlineExceeded:
		err = errors.New("service took too long to respond. Disconnecting client")
	// regular error without annotation
	case codes.Unknown:
		err = errors.New(st.Message())
	// likely means that IsAuthenticated got cancelled, so we need to keep the error intact
	case codes.Canceled:
		break
	case codes.PermissionDenied:
		// permission denied, just format it
		err = fmt.Errorf("permission denied: %v", st.Message())
	// grpc error, just format it
	default:
		err = fmt.Errorf("error %s from server: %v", st.Code(), st.Message())
	}
	return err
}
