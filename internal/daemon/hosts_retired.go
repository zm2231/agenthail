package daemon

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

type mirrorRetiredAfterKey struct{}

// A clock means nothing outside the epoch it was read in.
func (d *Daemon) withMirrorRetiredAfter(r *http.Request) context.Context {
	query := r.URL.Query()
	if !catalogMirror(r.Context()) || !query.Has("retiredAfter") {
		return r.Context()
	}
	after, err := strconv.ParseInt(query.Get("retiredAfter"), 10, 64)
	if err != nil || after < 0 {
		return r.Context()
	}
	epoch, _, err := d.Registry.CatalogState()
	if err != nil || epoch != query.Get("retiredEpoch") {
		return r.Context()
	}
	return context.WithValue(r.Context(), mirrorRetiredAfterKey{}, after)
}

func mirrorRetiredAfter(ctx context.Context) *int64 {
	after, ok := ctx.Value(mirrorRetiredAfterKey{}).(int64)
	if !ok {
		return nil
	}
	return &after
}

func (d *Daemon) mirrorSnapshotQuery(hostID string) (url.Values, error) {
	values := url.Values{"fresh": []string{"1"}}
	clock, err := d.Registry.HostRetiredClock(hostID)
	if err != nil {
		return nil, err
	}
	epoch, err := d.Registry.HostCatalogEpoch(hostID)
	if err != nil {
		return nil, err
	}
	if clock != nil && epoch != "" {
		values.Set("retiredAfter", strconv.FormatInt(*clock, 10))
		values.Set("retiredEpoch", epoch)
	}
	return values, nil
}

func heldRetired(snapshot remoteSnapshot) map[string]struct{} {
	held := make(map[string]struct{}, len(snapshot.RetiredIDs))
	for _, id := range snapshot.RetiredIDs {
		held[id] = struct{}{}
	}
	return held
}
