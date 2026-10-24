package surface

import "context"

func Tail(ctx context.Context, reader SessionReader, session *Session, limit int) ([]Exchange, error) {
	read, err := reader.ReadSession(ctx, session, SessionReadRequest{Limit: limit})
	if err != nil {
		return nil, err
	}
	return read.Exchanges, nil
}
