package teammates

import (
	"fmt"

	"github.com/lukaskoebe/sandbox-studio/internal/store"
)

// CheckHeartbeat validates a check-in schedule: every 1 to 24 hours, within a window of local
// hours that ends after it starts.
func CheckHeartbeat(h store.Heartbeat) error {
	switch {
	case h.EveryHours < 1 || h.EveryHours > 24:
		return fmt.Errorf("%w: check-ins are every 1 to 24 hours", ErrInvalid)
	case h.FromHour < 0 || h.FromHour > 23:
		return fmt.Errorf("%w: the window starts at an hour from 0 to 23", ErrInvalid)
	case h.ToHour < 1 || h.ToHour > 24:
		return fmt.Errorf("%w: the window ends at an hour from 1 to 24", ErrInvalid)
	case h.FromHour >= h.ToHour:
		return fmt.Errorf("%w: the window must end after it starts", ErrInvalid)
	}
	return nil
}
