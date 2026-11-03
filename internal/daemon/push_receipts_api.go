package daemon

import (
	"net/http"
	"regexp"
	"time"
)

const pushReceiptBatch = 50

var pushIDPattern = regexp.MustCompile(`^[a-z0-9]{8,64}$`)

type pushReceipt struct {
	PushID     string    `json:"pushId"`
	ReceivedAt time.Time `json:"receivedAt"`
	Alerts     string    `json:"alerts"`
}

// apiDevicePushReceiptsHandler takes the receipts a phone's notification
// service sends for pushes it received, possibly long after, when the phone
// was away from this computer at the time.
func (d *Daemon) apiDevicePushReceiptsHandler(w http.ResponseWriter, r *http.Request) {
	device, err := d.Registry.AuthenticateDevice(bearerToken(r), "read")
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "device_unauthorized", "A paired device token is required.")
		return
	}
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	defer r.Body.Close()
	var request struct {
		Receipts []pushReceipt `json:"receipts"`
	}
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil || len(request.Receipts) > pushReceiptBatch {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The push receipts are invalid.")
		return
	}
	now := time.Now()
	recorded := 0
	for _, receipt := range request.Receipts {
		if !pushIDPattern.MatchString(receipt.PushID) || (receipt.Alerts != "" && receipt.Alerts != "on" && receipt.Alerts != "off") {
			continue
		}
		at := receipt.ReceivedAt
		if at.IsZero() || at.After(now) {
			at = now
		}
		arrived, err := d.Registry.RecordPushReceipt(device.ID, receipt.PushID, at, receipt.Alerts)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "push_receipt_failed", err.Error())
			return
		}
		if arrived {
			recorded++
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]int{"recorded": recorded})
}
