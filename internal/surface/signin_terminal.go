package surface

import (
	"errors"

	"github.com/zm2231/agenthail/internal/signin"
)

func SigninTerminalKind(err error) DeliveryTerminalKind {
	var problem signin.ProblemError
	switch {
	case signin.IsOff(err):
		return DeliverySigninOff
	case signin.IsUnavailable(err):
		return DeliverySigninUnavailable
	case errors.As(err, &problem):
		return DeliverySigninUnreadable
	}
	return DeliveryAuthenticationNeeded
}
