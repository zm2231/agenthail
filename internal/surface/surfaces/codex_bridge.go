package surfaces

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const codexHookJS = `
(() => {
  if (typeof window?.electronBridge?.sendMessageFromView !== 'function') return 'no-electron-bridge';
  const current = globalThis.__agenthailRendererBridgeV3;
  if (current && typeof current.handler === 'function' && typeof current.request === 'function') return 'already';

  const bridge = current && typeof current.handler === 'function' ? current : {
    events: [],
    sequence: 0,
    handler: null,
    request: null
  };
  if (typeof bridge.handler !== 'function') {
    bridge.handler = function(event) {
      try {
        const value = event && event.data;
        if (value && value.type === 'mcp-notification' && value.hostId === 'local' && typeof value.method === 'string') {
          bridge.events.push({ sequence: ++bridge.sequence, method: value.method, params: value.params || {} });
          if (bridge.events.length > 1000) bridge.events.splice(0, bridge.events.length - 1000);
        }
      } catch {}
    };
    window.addEventListener('message', bridge.handler, true);
  }
  globalThis.__agenthailRendererBridgeV3 = bridge;

  const findRequestDispatcher = async () => {
    const queue = [...document.scripts].map(script => script.src).filter(Boolean);
    const seen = new Set();
    while (queue.length && seen.size < 400) {
      const url = queue.shift();
      if (seen.has(url)) continue;
      seen.add(url);
      try {
        const source = await fetch(url).then(response => response.text());
        if (source.includes('Missing AppServer request message handler')) {
          const module = await import(url);
          for (const value of Object.values(module)) {
            if (typeof value !== 'function' || value.length !== 2) continue;
            const text = Function.prototype.toString.call(value);
            if (/return\s+[A-Za-z_$][\w$]*\.sendRequest\([A-Za-z_$][\w$]*,[A-Za-z_$][\w$]*\)/.test(text)) return value;
          }
        }
        const imports = /(?:from\s*|import\s*\(|import\s*)["']([^"']+\.js)["']/g;
        let match;
        while ((match = imports.exec(source))) queue.push(new URL(match[1], url).href);
        for (const asset of source.matchAll(/["'](\.\/[^"']+\.js)["']/g)) queue.push(new URL(asset[1], url).href);
      } catch {}
    }
    return null;
  };
  return findRequestDispatcher().then(dispatcher => {
    if (typeof dispatcher !== 'function') return 'no-request-dispatcher';
    bridge.request = (method, params, timeoutMs) => dispatcher('send-cli-request-for-host', {
      hostId: 'local', method, params, timeoutMs, source: 'agenthail'
    });
    return 'hooked';
  });
})()
`

func codexRendererTrampolineJS(expression string) string {
	return fmt.Sprintf(`(async()=>{const electron=process.getBuiltinModule('module').createRequire(process.execPath)('electron');const view=electron.webContents.getAllWebContents().find(x=>(x.getType()==='window'||x.getType()==='page')&&x.getURL().startsWith('app://'));if(!view)throw new Error('Codex renderer webContents not found');return await view.executeJavaScript(%s)})()`, strconvQuote(expression))
}

func codexRPCJSONJS(method, paramsJSON string, timeout time.Duration) string {
	return fmt.Sprintf(`(async()=>{try{const b=globalThis.__agenthailRendererBridgeV3;if(!b||typeof b.request!=='function')return JSON.stringify({error:{code:'bridge_unavailable',message:'Codex Desktop request dispatcher is unavailable'}});const result=await b.request(%s,%s,%d);return JSON.stringify({result})}catch(e){return JSON.stringify({error:{code:e&&e.code!=null?e.code:'desktop_error',message:e&&e.message?e.message:String(e)}})}})()`,
		strconvQuote(method), paramsJSON, timeout.Milliseconds())
}

func codexPayloadInitJS(id string) string {
	return fmt.Sprintf(`(()=>{globalThis.__agenthailPayloads=globalThis.__agenthailPayloads||Object.create(null);globalThis.__agenthailPayloads[%s]='';return'ok'})()`, strconvQuote(id))
}

func codexPayloadAppendJS(id, chunk string) string {
	return fmt.Sprintf(`(()=>{const p=globalThis.__agenthailPayloads;if(!p||typeof p[%s]!=='string')return'missing';p[%s]+=%s;return'ok'})()`, strconvQuote(id), strconvQuote(id), strconvQuote(chunk))
}

func codexPayloadDeleteJS(id string) string {
	return fmt.Sprintf(`(()=>{const p=globalThis.__agenthailPayloads;if(p)delete p[%s];return'ok'})()`, strconvQuote(id))
}

func codexStagedRPCJS(id, method string, timeout time.Duration) string {
	return fmt.Sprintf(`(async()=>{try{const b=globalThis.__agenthailRendererBridgeV3,p=globalThis.__agenthailPayloads;if(!b||typeof b.request!=='function')return JSON.stringify({error:{code:'bridge_unavailable',message:'Codex Desktop request dispatcher is unavailable'}});if(!p||typeof p[%s]!=='string')return JSON.stringify({error:{code:'missing_payload',message:'staged request payload is unavailable'}});const encoded=p[%s];delete p[%s];const bytes=Uint8Array.from(atob(encoded),c=>c.charCodeAt(0));const params=JSON.parse(new TextDecoder().decode(bytes));const result=await b.request(%s,params,%d);return JSON.stringify({result})}catch(e){return JSON.stringify({error:{code:e&&e.code!=null?e.code:'desktop_error',message:e&&e.message?e.message:String(e)}})}})()`, strconvQuote(id), strconvQuote(id), strconvQuote(id), strconvQuote(method), timeout.Milliseconds())
}

const codexEventCursorJS = `(()=>{const b=globalThis.__agenthailRendererBridgeV3;return b?b.sequence:0})()`

func codexEventsJS(after int64) string {
	return fmt.Sprintf(`(()=>{const b=globalThis.__agenthailRendererBridgeV3;if(!b)return JSON.stringify({cursor:0,events:[]});return JSON.stringify({cursor:b.sequence,events:b.events.filter(x=>x.sequence>%d)})})()`, after)
}

func strconvQuote(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func (c *Codex) ensureHooked(ctx context.Context, conn *cdpConn) error {
	var lastResult string
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		value, err := conn.evaluate(ctx, codexHookJS, 5*time.Second)
		if err == nil {
			lastResult, _ = value.(string)
			if lastResult == "hooked" || lastResult == "already" {
				return nil
			}
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if lastResult == "no-electron-bridge" {
		return fmt.Errorf("Codex renderer does not expose the Desktop app-server bridge; update Codex Desktop or relaunch it with 'agenthail launch codex'")
	}
	if lastResult == "no-request-dispatcher" {
		return fmt.Errorf("Codex Desktop's request dispatcher could not be discovered; this Desktop build is not yet supported by Agenthail")
	}
	if lastErr != nil {
		return fmt.Errorf("install Codex renderer bridge: %w", lastErr)
	}
	return fmt.Errorf("install Codex renderer bridge: unexpected result %q after 3 attempts", lastResult)
}

func (c *Codex) rpc(ctx context.Context, conn *cdpConn, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	payloadID := "agenthail-" + uuid.NewString()
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode %s params: %w", method, err)
	}
	expression := codexRPCJSONJS(method, string(paramsJSON), wait)
	if len(paramsJSON) > 2048 {
		staged := false
		defer func() {
			if staged {
				_, _ = conn.evaluate(context.Background(), codexPayloadDeleteJS(payloadID), 500*time.Millisecond)
			}
		}()
		if value, stageErr := conn.evaluate(ctx, codexPayloadInitJS(payloadID), 2*time.Second); stageErr != nil || value != "ok" {
			return nil, fmt.Errorf("stage %s params: %v (%v)", method, stageErr, value)
		}
		staged = true
		encoded := base64.StdEncoding.EncodeToString(paramsJSON)
		for start := 0; start < len(encoded); start += 2048 {
			end := start + 2048
			if end > len(encoded) {
				end = len(encoded)
			}
			if value, stageErr := conn.evaluate(ctx, codexPayloadAppendJS(payloadID, encoded[start:end]), 2*time.Second); stageErr != nil || value != "ok" {
				return nil, fmt.Errorf("stage %s params chunk: %v (%v)", method, stageErr, value)
			}
		}
		expression = codexStagedRPCJS(payloadID, method, wait)
	}
	value, err := conn.evaluate(ctx, expression, wait+2*time.Second)
	if err != nil {
		return nil, err
	}
	raw, _ := value.(string)
	var response map[string]any
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, fmt.Errorf("parse %s response: %w", method, err)
	}
	if envelope, ok := response["error"].(map[string]any); ok {
		message, _ := envelope["message"].(string)
		code := envelope["code"]
		return nil, fmt.Errorf("%s RPC error (%v): %s", method, code, message)
	}
	return response, nil
}

type codexEvent struct {
	Sequence int64          `json:"sequence"`
	Method   string         `json:"method"`
	Params   map[string]any `json:"params"`
}

type codexEventBatch struct {
	Cursor int64        `json:"cursor"`
	Events []codexEvent `json:"events"`
}

func codexContainsID(value any, ids ...string) bool {
	switch current := value.(type) {
	case string:
		for _, id := range ids {
			if id != "" && current == id {
				return true
			}
		}
	case map[string]any:
		for _, child := range current {
			if codexContainsID(child, ids...) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if codexContainsID(child, ids...) {
				return true
			}
		}
	}
	return false
}

func codexEventText(value any) string {
	switch current := value.(type) {
	case map[string]any:
		for _, key := range []string{"delta", "text", "message"} {
			if text, ok := current[key].(string); ok && text != "" {
				return text
			}
		}
		for _, child := range current {
			if text := codexEventText(child); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range current {
			if text := codexEventText(child); text != "" {
				return text
			}
		}
	}
	return ""
}

func codexEventTool(value any) string {
	if current, ok := value.(map[string]any); ok {
		for _, key := range []string{"toolName", "name"} {
			if name, ok := current[key].(string); ok && name != "" {
				return name
			}
		}
		for _, child := range current {
			if name := codexEventTool(child); name != "" {
				return name
			}
		}
	}
	return ""
}

func codexCompletionMethod(method string) bool {
	lower := strings.ToLower(method)
	return strings.Contains(lower, "turn/completed") || strings.Contains(lower, "turn/completion") || strings.Contains(lower, "turn.completed")
}
