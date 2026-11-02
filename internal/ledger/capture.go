package ledger

type CaptureInput struct {
	ProjectKey string
	MessageID  string
	SessionID  string
	TargetID   string
	Surface    string
	Text       string
}

func PersonCapture(store *Store) func(CaptureInput) error {
	return func(input CaptureInput) error {
		if input.ProjectKey == "" || input.Text == "" {
			return nil
		}
		enabled, err := store.Enabled(input.ProjectKey)
		if err != nil {
			return err
		}
		if !enabled {
			return nil
		}
		_, err = store.RecordUtterance(input.ProjectKey, input.MessageID, input.SessionID, input.TargetID, input.Surface, input.Text)
		return err
	}
}
