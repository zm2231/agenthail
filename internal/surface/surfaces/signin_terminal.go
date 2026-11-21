package surfaces

import (
	"errors"

	"github.com/zm2231/agenthail/internal/signin"
	"github.com/zm2231/agenthail/internal/surface"
)

func signinTerminalKind(err error) surface.DeliveryTerminalKind {
	var problem signin.ProblemError
	switch {
	case signin.IsOff(err):
		return surface.DeliverySigninOff
	case signin.IsUnavailable(err):
		return surface.DeliverySigninUnavailable
	case errors.As(err, &problem):
		return surface.DeliverySigninUnreadable
	}
	return surface.DeliveryAuthenticationNeeded
}
