package registry

import "github.com/zm2231/agenthail/internal/surface"

type SessionPlace struct {
	Cwd        string
	Transcript string
}

func (r *Registry) LocalSessionPlaces(kind surface.SurfaceKind) ([]SessionPlace, error) {
	rows, err := r.read.Query(`SELECT DISTINCT cwd,transcript FROM sessions WHERE host_id='' AND surface=? AND cwd!='' AND transcript!=''`, string(kind))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var places []SessionPlace
	for rows.Next() {
		var place SessionPlace
		if err := rows.Scan(&place.Cwd, &place.Transcript); err != nil {
			return nil, err
		}
		places = append(places, place)
	}
	return places, rows.Err()
}
