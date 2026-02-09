//go:build withgoogle

package consts

const (
	// DbusName owned by the broker for authd to contact us.
	DbusName = "com.ubuntu.authd.Google"
	// DbusObject main object path for authd to contact us.
	DbusObject = "/com/ubuntu/authd/Google"
)
