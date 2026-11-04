package daemon

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func attachmentViews(stored surface.Uploads) []map[string]any {
	if len(stored) == 0 {
		return nil
	}
	views := make([]map[string]any, 0, len(stored))
	for _, upload := range stored {
		views = append(views, uploadView(upload))
	}
	return views
}

func (d *Daemon) handBack(stored surface.Uploads) []map[string]any {
	views := attachmentViews(stored)
	for index, upload := range stored {
		if _, err := d.uploads.Resolve(upload.ID); err != nil {
			views[index]["missing"] = true
		}
	}
	return views
}

func (d *Daemon) storedUploads(queued surface.Uploads) (surface.Uploads, error) {
	var resolved surface.Uploads
	for _, upload := range queued {
		current, err := d.uploads.Resolve(upload.ID)
		if err != nil {
			return nil, surface.DeliveryTerminal(fmt.Errorf("attached file %s is no longer stored; attach it again", upload.Name), surface.DeliveryInvalidRequest)
		}
		resolved = append(resolved, current)
	}
	return resolved, nil
}
