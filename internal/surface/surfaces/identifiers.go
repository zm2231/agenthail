package surfaces

import "github.com/google/uuid"

func looksLikeUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}
