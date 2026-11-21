package surface

import "github.com/google/uuid"

func LooksLikeUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}
