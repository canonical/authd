package errno

import "errors"

// Our code is not racing (as these tests prove), but errno is, so we're writing on a
// variable here to check that the code we have is still race-proof.
var testErrno int

func init() {
	getErrno = func() int { return testErrno }
	unsetErrno = func() { testErrno = 0 }
}

// Set sets the errno to the err value. It's only used for testing.
func Set(err error) {
	if mu.TryLock() {
		mu.Unlock()
		panic("Using errno without locking!")
	}

	var errno Error
	if err != nil && !errors.As(err, &errno) {
		panic("Not a valid errno value")
	}
	testErrno = int(errno)
}
