package ledger

import "errors"

type AttachOutcome struct {
	AttachResult
	Refused string `json:"refused,omitempty"`
}

type AttachEachResult struct {
	ProjectKey string          `json:"projectKey"`
	Fragments  []AttachOutcome `json:"fragments"`
}

// AttachEach attaches several fragments in one call, each on its own, so a refused fragment leaves the others recorded and says why it was refused.
func (s *Store) AttachEach(projectKey, sessionID string, fragments []AttachRequest) (AttachEachResult, error) {
	if len(fragments) == 0 {
		return AttachEachResult{}, refuse("attach needs at least one fragment")
	}
	out := AttachEachResult{ProjectKey: projectKey, Fragments: make([]AttachOutcome, 0, len(fragments))}
	for _, f := range fragments {
		f.ProjectKey, f.SessionID = projectKey, sessionID
		result, err := s.Attach(f)
		var refusal *Refusal
		switch {
		case errors.As(err, &refusal):
			out.Fragments = append(out.Fragments, AttachOutcome{Refused: refusal.Reason})
		case err != nil:
			return out, err
		default:
			out.Fragments = append(out.Fragments, AttachOutcome{AttachResult: result})
		}
	}
	return out, nil
}
