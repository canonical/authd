package broker

type isAuthenticatedDataResponse interface {
	isAuthenticatedDataResponse()
}

// userInfoMessage represents the user information message that is returned to authd.
type userInfoMessage struct {
	UserInfo userInfo `json:"userinfo"`
}

func (userInfoMessage) isAuthenticatedDataResponse() {}

// errorMessage represents the error message that is returned to authd.
type errorMessage struct {
	Message string `json:"message"`
}

func (errorMessage) isAuthenticatedDataResponse() {}
