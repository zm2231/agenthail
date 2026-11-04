package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/surface"
)

type imageSteerSurface struct {
	*imageSurface
	steeredImages []surface.Uploads
}

func (f *imageSteerSurface) SteerWithImages(_ context.Context, _ *surface.Session, message string, images surface.Uploads) error {
	f.steered = append(f.steered, message)
	f.steeredImages = append(f.steeredImages, images)
	return nil
}

func steerImageFixture(t *testing.T, takesImages bool) (*Daemon, *imageSteerSurface, http.Handler) {
	t.Helper()
	d, adapter, _ := uploadFixture(t)
	adapter.accepted = false
	adapter.caps.SteerImages = takesImages
	steering := &imageSteerSurface{imageSurface: adapter}
	d.Surfaces = []surface.Surface{steering}
	if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: deliverypolicy.Steer}); err != nil {
		t.Fatal(err)
	}
	d.Registry.SetBusyPolicy(deliverypolicy.Load)
	return d, steering, d.dashboardHandler(&dashboardServer{token: dashboardTestToken})
}

func sendForEvidence(t *testing.T, handler http.Handler, body map[string]any) (string, string) {
	t.Helper()
	response := postAction(t, handler, body)
	var answer struct {
		Result struct {
			Evidence string `json:"evidence"`
			Detail   string `json:"detail"`
		} `json:"result"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil {
		t.Fatalf("send status=%d body=%s", response.Code, response.Body.String())
	}
	return answer.Result.Evidence, answer.Result.Detail
}

func personSend(message string, attachments ...string) map[string]any {
	body := map[string]any{"action": "send", "sessionId": "to", "message": message, "author": "person"}
	if len(attachments) > 0 {
		body["attachments"] = attachments
	}
	return body
}

func TestAnImageSteersARunningTurnOnASessionWhoseSteerTakesImages(t *testing.T) {
	d, adapter, handler := steerImageFixture(t, true)
	picture := decodeUpload(t, postUpload(t, handler, "to", "shot.png", "image/png", testPNG(t, 4, 4)))
	notes := decodeUpload(t, postUpload(t, handler, "to", "notes.txt", "text/plain", []byte("notes")))

	if evidence, _ := sendForEvidence(t, handler, personSend("look at this", picture.Upload.ID, notes.Upload.ID)); evidence == "queued" {
		t.Fatalf("the image was held for evidence=%q", evidence)
	}
	if len(adapter.steered) != 1 || len(adapter.steeredImages) != 1 || len(adapter.steeredImages[0]) != 1 || adapter.steeredImages[0][0].Name != "shot.png" {
		t.Fatalf("steered=%q images=%+v", adapter.steered, adapter.steeredImages)
	}
	if !strings.HasPrefix(adapter.steered[0], "look at this") || !strings.Contains(adapter.steered[0], "notes.txt") || strings.Contains(adapter.steered[0], "shot.png") {
		t.Fatalf("steer text=%q, want the file named and the image carried as an image", adapter.steered[0])
	}
	if count := d.Registry.QueueCount("to"); count != 0 {
		t.Fatalf("queued=%d", count)
	}
}

func TestAnImageWaitsOnASessionWhoseSteerTakesNoneAndLaterSendsStayBehindIt(t *testing.T) {
	d, adapter, handler := steerImageFixture(t, false)
	picture := decodeUpload(t, postUpload(t, handler, "to", "shot.png", "image/png", testPNG(t, 4, 4)))

	evidence, detail := sendForEvidence(t, handler, personSend("look at this", picture.Upload.ID))
	if evidence != "queued" || !strings.Contains(detail, "attached images can't join a running turn") {
		t.Fatalf("evidence=%q detail=%q", evidence, detail)
	}
	if evidence, _ := sendForEvidence(t, handler, personSend("and this")); evidence != "queued" {
		t.Fatalf("the later message went ahead with evidence=%q", evidence)
	}
	if len(adapter.steered) != 0 {
		t.Fatalf("steered past the held image: %q", adapter.steered)
	}
	claimed, err := d.Registry.ClaimNextSteerMessage("to", time.Now())
	if err != nil || claimed != nil {
		t.Fatalf("a steer claim took %+v err=%v, want the row behind the image to stay behind it", claimed, err)
	}

	explicit := personSend("right now")
	explicit["busyDelivery"] = "steer"
	if evidence, _ := sendForEvidence(t, handler, explicit); evidence == "queued" {
		t.Fatalf("an explicit steer was held")
	}
	if len(adapter.steered) != 1 || adapter.steered[0] != "right now" {
		t.Fatalf("steered=%q", adapter.steered)
	}
}

func TestAnotherSendersQueuedRowDoesNotHoldASteer(t *testing.T) {
	d, adapter, handler := steerImageFixture(t, false)
	if _, err := d.Registry.QueueMessageWithOptions("to", "from elsewhere", "other-sender", surface.SendOptions{BusyDelivery: "queue", SourceSessionID: "from"}); err != nil {
		t.Fatal(err)
	}
	if evidence, _ := sendForEvidence(t, handler, personSend("go on")); evidence == "queued" {
		t.Fatalf("another sender's row held this one")
	}
	if len(adapter.steered) != 1 {
		t.Fatalf("steered=%q", adapter.steered)
	}
}

func TestAFileSteersAsItsPathOnASessionWhoseSteerTakesNoImages(t *testing.T) {
	_, adapter, handler := steerImageFixture(t, false)
	notes := decodeUpload(t, postUpload(t, handler, "to", "notes.txt", "text/plain", []byte("notes")))
	if evidence, _ := sendForEvidence(t, handler, personSend("read this", notes.Upload.ID)); evidence == "queued" {
		t.Fatalf("a file was held")
	}
	if len(adapter.steered) != 1 || !strings.Contains(adapter.steered[0], "notes.txt") {
		t.Fatalf("steered=%q", adapter.steered)
	}
}

func TestAQueuedImageSteersFromTheQueueWhenTheSessionTakesIt(t *testing.T) {
	d, adapter, handler := steerImageFixture(t, true)
	picture := decodeUpload(t, postUpload(t, handler, "to", "shot.png", "image/png", testPNG(t, 4, 4)))
	stored, err := d.resolveUploads([]string{picture.Upload.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Registry.QueueMessageWithOptions("to", "from the queue", "steer-image", surface.SendOptions{BusyDelivery: "steer", SteersUploads: true, Uploads: stored, Author: surface.AuthorPerson}); err != nil {
		t.Fatal(err)
	}
	session, err := d.Registry.Session("to")
	if err != nil {
		t.Fatal(err)
	}
	session.Status = surface.StatusBusy
	d.drainSteerMessageQueue(t.Context(), adapter, session)
	if len(adapter.steeredImages) != 1 || len(adapter.steeredImages[0]) != 1 || adapter.steered[0] != "from the queue" {
		t.Fatalf("steered=%q images=%+v", adapter.steered, adapter.steeredImages)
	}
}
