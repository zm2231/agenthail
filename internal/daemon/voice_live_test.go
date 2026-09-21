//go:build voice_live

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/voice"
)

// TestLiveCodexVoice captures a manually reviewed, paid conversational evaluation.
// Spoken user recordings enter the production peer and real Codex Voice. Existing
// test-owned tasks do real work; semantic acceptance requires reviewing their
// commands, results, and the spoken summary, not matching a canned reply.
func TestLiveCodexVoice(t *testing.T) {
	if os.Getenv("AGENTHAIL_LIVE_VOICE") != "1" {
		t.Skip("requires explicit live voice authorization")
	}
	audioPath := os.Getenv("AGENTHAIL_VOICE_SMOKE_AUDIO")
	followupPath := os.Getenv("AGENTHAIL_VOICE_SMOKE_FOLLOWUP_AUDIO")
	workerID := os.Getenv("AGENTHAIL_VOICE_SMOKE_WORKER")
	if audioPath == "" || followupPath == "" || workerID == "" {
		t.Fatal("listing audio, followup audio, and an existing test-owned worker ID are required")
	}
	codex := surfaces.NewCodex(os.Getenv("AGENTHAIL_CODEX_REMOTE"))
	statePath := os.Getenv("AGENTHAIL_VOICE_SMOKE_STATE")
	if statePath == "" {
		statePath = filepath.Join(t.TempDir(), "operator.json")
	}
	commandPath := os.Getenv("AGENTHAIL_VOICE_SMOKE_CLI")
	if commandPath == "" || !filepath.IsAbs(commandPath) {
		t.Fatal("AGENTHAIL_VOICE_SMOKE_CLI must name the matching built CLI by absolute path")
	}
	s := voice.New(statePath, &surfaces.CodexVoice{Codex: codex}, nil, commandPath)
	if s.View("").Session == nil {
		t.Fatal("provide an existing test-owned operator state; this evaluation must not create tasks")
	}
	baselineContext, baselineCancel := context.WithTimeout(context.Background(), 20*time.Second)
	baseline, err := codex.ReadSession(baselineContext, s.View("").Session, surface.SessionReadRequest{})
	baselineCancel()
	if err != nil {
		t.Fatal(err)
	}
	priorItems := make(map[string]bool)
	for _, item := range baseline.Items {
		priorItems[item.ID] = true
	}
	workerSession := &surface.Session{ID: workerID, Surface: surface.KindCodex}
	workerBaseline, err := codex.ReadSession(context.Background(), workerSession, surface.SessionReadRequest{})
	if err != nil || workerBaseline.UnavailableReason != "" {
		t.Fatalf("existing worker activity unavailable: %v", err)
	}
	priorWorkerItems := make(map[string]bool)
	for _, item := range workerBaseline.Items {
		priorWorkerItems[item.ID] = true
	}
	token := uuid.NewString()
	result := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "probe", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><meta name=viewport content="width=device-width,initial-scale=1"><h1>Live Codex Voice evaluation</h1><p>Existing test-owned operator. Synthesized fixture audio, no microphone recording. Starts real Codex inference. List existing tasks, then select and message the existing test task. No creation.</p><button id=start>Speak listing request</button><button id=followup>Speak followup request</button><button id=stop>Hang up</button><pre id=status></pre><audio autoplay></audio><script>`+liveVoiceHarness+voicePeerScript+`</script>`)
	})
	authorized := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("probe")
			if err != nil || cookie.Value != token {
				http.Error(w, "unauthorized", 401)
				return
			}
			if r.Method != "GET" && !sameOrigin(r) {
				http.Error(w, "cross origin", 403)
				return
			}
			r.Header.Set("Authorization", "Bearer "+token)
			next(w, r)
		}
	}
	mux.HandleFunc("/smoke/api", authorized(voiceHandler(s)))
	mux.HandleFunc("/smoke/audio", authorized(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, audioPath) }))
	mux.HandleFunc("/smoke/followup-audio", authorized(func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, followupPath) }))
	mux.HandleFunc("/smoke/finish", authorized(func(w http.ResponseWriter, r *http.Request) {
		var receipt struct {
			InboundBytes uint64  `json:"inboundBytes"`
			AudioEnergy  float64 `json:"audioEnergy"`
			WorkerID     string  `json:"workerId"`
		}
		if r.Method != "POST" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&receipt) != nil || receipt.InboundBytes == 0 || receipt.AudioEnergy <= 0 {
			http.Error(w, "no received audio evidence", 400)
			return
		}
		if _, err := uuid.Parse(receipt.WorkerID); err != nil || receipt.WorkerID != workerID {
			http.Error(w, "the preselected existing test-owned worker ID is required", 400)
			return
		}
		v := s.View(fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
		var finalSequence, spokenSequence int64
		for _, e := range v.Events {
			if e.Method == "item/completed" {
				finalSequence = e.Sequence
			}
			if e.Method == "thread/realtime/transcript/delta" && e.Params["role"] == "assistant" {
				spokenSequence = e.Sequence
			}
		}
		if finalSequence == 0 || spokenSequence <= finalSequence {
			http.Error(w, "no observed speech after the completed operator answer", 409)
			return
		}
		t.Logf("AUDIO RECEIPT bytes=%d energy=%f", receipt.InboundBytes, receipt.AudioEnergy)
		select {
		case result <- receipt.WorkerID:
		default:
		}
		fmt.Fprint(w, "ok")
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	t.Logf("OPEN %s (loopback only; temporary browser harness)", server.URL)
	defer func() {
		v := s.View(fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
		if v.Session != nil {
			t.Logf("TEST-OWNED OPERATOR %s", v.Session.ID)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := s.Apply(ctx, fmt.Sprintf("%x", sha256.Sum256([]byte(token))), voice.Action{Action: "stop", AttemptID: v.AttemptID})
			if err != nil {
				t.Errorf("hangup not confirmed: %v", err)
			}
		}
	}()
	select {
	case <-result:
	case <-time.After(8 * time.Minute):
		t.Fatal("no completed browser audio and spoken-reply receipt within eight minutes")
	}
	v := s.View(fmt.Sprintf("%x", sha256.Sum256([]byte(token))))
	if v.Session == nil {
		t.Fatal("no durable operator identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	timeline, err := codex.ReadSession(ctx, v.Session, surface.SessionReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var listEvidence, sendEvidence, workerEvidence bool
	var operatorItems []surface.TimelineItem
	for _, item := range timeline.Items {
		if priorItems[item.ID] {
			continue
		}
		operatorItems = append(operatorItems, item)
		if item.Kind == "toolCall" && strings.Contains(item.Text, "agenthail") {
			if strings.Contains(item.Text, "thread create") {
				t.Fatal("evaluation created a task")
			}
			listEvidence = listEvidence || strings.Contains(item.Text, " list")
			sendEvidence = sendEvidence || strings.Contains(item.Text, " send")
		}
		if item.Kind == "toolResult" && strings.Contains(item.Text, workerID) {
			workerEvidence = true
		}
	}
	if !listEvidence || !sendEvidence || !workerEvidence || workerID == v.Session.ID {
		t.Fatalf("handoff evidence incomplete: list=%v send=%v worker=%v", listEvidence, sendEvidence, workerEvidence)
	}
	output, err := exec.CommandContext(ctx, commandPath, "last", workerID, "3", "--full", "--json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Session   string `json:"session"`
		Exchanges []struct {
			Assistant string `json:"assistant"`
		} `json:"exchanges"`
	}
	if json.Unmarshal(output, &reply) != nil || reply.Session != workerID {
		t.Fatal("worker reply identity missing")
	}
	workerTimeline, err := codex.ReadSession(ctx, workerSession, surface.SessionReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var workerItems []surface.TimelineItem
	var workerTool, workerAnswer bool
	for _, item := range workerTimeline.Items {
		if priorWorkerItems[item.ID] {
			continue
		}
		workerItems = append(workerItems, item)
		workerTool = workerTool || item.Kind == "toolCall"
		workerAnswer = workerAnswer || (item.Role == "assistant" && strings.TrimSpace(item.Text) != "")
	}
	if !workerTool || !workerAnswer {
		t.Fatal("the existing worker must perform real tool work and return an answer")
	}
	data, err := json.MarshalIndent(map[string]any{"operatorId": v.Session.ID, "workerId": workerID, "attemptId": v.AttemptID, "voiceEvents": v.Events, "operatorActivity": operatorItems, "workerActivity": workerItems, "workerReply": json.RawMessage(output), "semanticReview": "required: compare requested work, actual commands and results, and spoken summary", "physicalIOS": "not tested"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(filepath.Dir(statePath), "dialogue-"+v.AttemptID+".json")
	if err := os.WriteFile(receiptPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("CAPTURED real voice dialogue and worker execution: %s; review scenario semantics before acceptance", receiptPath)
}

const liveVoiceHarness = `
let probePeer, probeAudio, probeContext, probeDestination, attempt, answer, played=false, timer;
const status=document.getElementById('status');
const OriginalPeer=RTCPeerConnection;
window.RTCPeerConnection=class extends OriginalPeer { constructor(...args){super(...args);probePeer=this;} };
navigator.mediaDevices.getUserMedia=async()=>{
 const context=new AudioContext();probeContext=context;await context.resume();const response=await fetch('/smoke/audio');
 const buffer=await context.decodeAudioData(await response.arrayBuffer());
 const source=context.createBufferSource();source.buffer=buffer;
 const destination=context.createMediaStreamDestination();probeDestination=destination;source.connect(destination);probeAudio=source;
 const silence=context.createConstantSource();silence.offset.value=0;silence.connect(destination);silence.start();
 return destination.stream;
};
async function api(body){const r=await fetch('/smoke/api',{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const value=await r.json();if(!r.ok)throw Error(JSON.stringify(value));return value;}
window.webkit={messageHandlers:{voice:{postMessage:async({type,value})=>{
 try {
  if(type==='offer') await api({action:'start',attemptId:attempt,sdp:value});
  if(type==='channel'&&value==='open'&&!played){played=true;await api({action:'connected',attemptId:attempt});probeAudio.start();}
  if(type==='error')status.textContent+='\nAUDIO ERROR '+value;
 }catch(error){status.textContent+='\n'+error.message;}
}}}};
document.getElementById('start').onclick=async()=>{
 document.getElementById('start').disabled=true;
 try { await api({action:'prepare'});attempt=crypto.randomUUID();await window.agenthailVoice.start();
 timer=setInterval(async()=>{try{const s=await api();status.textContent=JSON.stringify(s,null,2);if(s.sdp&&s.sdp!==answer){answer=s.sdp;await window.agenthailVoice.answer(answer);}}catch(error){status.textContent=error.message;}},1000);
 }catch(error){status.textContent=error.message;}
};
document.getElementById('stop').onclick=async()=>{window.agenthailVoice.end();clearInterval(timer);await api({action:'stop',attemptId:attempt});};
document.getElementById('followup').onclick=async()=>{
 const response=await fetch('/smoke/followup-audio');const buffer=await probeContext.decodeAudioData(await response.arrayBuffer());
 const source=probeContext.createBufferSource();source.buffer=buffer;source.connect(probeDestination);source.start();
};
window.finishProbe=async(workerId)=>{
 const stats=await probePeer.getStats();let inboundBytes=0,audioEnergy=0;
 for(const s of stats.values())if(s.type==='inbound-rtp'&&s.kind==='audio'){inboundBytes+=s.bytesReceived||0;audioEnergy+=s.totalAudioEnergy||0;}
 const response=await fetch('/smoke/finish',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({inboundBytes,audioEnergy,workerId})});return {status:response.status,message:await response.text(),inboundBytes,audioEnergy};
};
`
