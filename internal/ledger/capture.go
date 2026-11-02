package ledger

type CaptureInput struct {
	ProjectKey string
	MessageID  string
	SessionID  string
	Surface    string
	Text       string
}

func PersonCapture(store *Store) func(CaptureInput) {
	return func(input CaptureInput) {
		if input.ProjectKey == "" || input.Text == "" {
			return
		}
		enabled, err := store.Enabled(input.ProjectKey)
		if err != nil || !enabled {
			return
		}
		store.RecordUtterance(input.ProjectKey, input.MessageID, input.SessionID, input.Surface, input.Text)
	}
}
