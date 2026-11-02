package ledger

func PersonCapture(store *Store) func(Utterance) error {
	return func(u Utterance) error {
		if u.ProjectKey == "" || u.Text == "" {
			return nil
		}
		enabled, err := store.Enabled(u.ProjectKey)
		if err != nil || !enabled {
			return err
		}
		_, err = store.RecordUtterance(u)
		return err
	}
}
