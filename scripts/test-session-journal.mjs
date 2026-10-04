import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../internal/daemon/dashboard/app.js', import.meta.url), 'utf8');
const stopStart = source.indexOf('function stopLiveStream(');
const stopEnd = source.indexOf('function renderLiveTurn(', stopStart);
const startStart = source.indexOf('function startLiveStream()');
const startEnd = source.indexOf('function resizeComposer(', startStart);
assert.ok(stopStart >= 0 && stopEnd > stopStart && startStart >= 0 && startEnd > startStart);

const elements = new Map();
const rendered = {chat: 0, context: 0};
const reloaded = [];
const instances = [];
class MockEventSource {
  constructor(url) {
    this.url = url;
    this.closed = false;
    this.listeners = new Map();
    instances.push(this);
  }
  addEventListener(name, handler) { this.listeners.set(name, handler); }
  close() { this.closed = true; }
  emit(name, data) {
    const handler = this.listeners.get(name);
    if (handler) handler({data: JSON.stringify({data})});
  }
}

const history = () => ({capabilities: {stream: true}, journalSeq: 42, timeline: {items: []}, exchanges: []});
const app = {
  selected: {id: 'session-a', surface: 'codex'},
  history: history(),
  liveSource: null,
  liveSessionID: '',
  liveText: '',
  liveTools: [],
};
const context = vm.createContext({
  app,
  EventSource: MockEventSource,
  location: {hash: '#conversations'},
  document: {hidden: false},
  mobileConversationView: () => false,
  renderChat: () => { rendered.chat++; },
  renderContextUsage: () => { rendered.context++; },
  selectSession: id => { reloaded.push(id); },
  $: selector => elements.get(selector),
});
vm.runInContext(source.slice(stopStart, stopEnd) + source.slice(startStart, startEnd), context);

vm.runInContext('startLiveStream()', context);
vm.runInContext('startLiveStream()', context);
assert.equal(instances.length, 1, 'same session must have one EventSource');
assert.equal(instances[0].url, '/api/session-stream?id=session-a&after=42');

instances[0].emit('item', {itemId: 'user-1', op: 'upsert', kind: 'text', role: 'user', body: 'question'});
instances[0].emit('item', {itemId: 'assistant-1', op: 'upsert', kind: 'text', role: 'assistant', body: 'answer'});
instances[0].emit('item', {itemId: 'assistant-1', op: 'upsert', kind: 'text', role: 'assistant', body: 'updated answer'});
assert.equal(app.history.timeline.items.length, 2, 'upsert must not duplicate a stable item id');
assert.equal(app.history.timeline.items[1].text, 'updated answer');
assert.equal(app.history.exchanges.length, 1);
assert.equal(app.history.exchanges[0].user, 'question');
assert.equal(app.history.exchanges[0].assistant, 'updated answer');

instances[0].emit('item', {itemId: 'context-1', op: 'upsert', kind: 'context', context: {usedTokens: 90}});
assert.equal(app.history.context.usedTokens, 90);
assert.equal(rendered.context, 1);
instances[0].emit('item', {itemId: 'source', op: 'reset', kind: 'source-error', reason: 'Unavailable'});
assert.equal(app.history.transcriptWarning, 'Unavailable');
instances[0].emit('item', {itemId: 'assistant-1', op: 'upsert', kind: 'text', role: 'assistant', body: 'recovered'});
assert.equal(app.history.transcriptWarning, '');

const oldSource = instances[0];
app.selected = {id: 'session-b', surface: 'codex'};
app.history = history();
app.history.journalSeq = 7;
vm.runInContext('startLiveStream()', context);
assert.equal(instances.length, 2);
assert.equal(oldSource.closed, true, 'switching sessions must close the old source');
oldSource.emit('item', {itemId: 'late-old', op: 'upsert', kind: 'text', role: 'assistant', body: 'stale'});
assert.equal(app.history.timeline.items.length, 0, 'late events from the old session must be ignored');
instances[1].emit('item', {itemId: 'new-1', op: 'upsert', kind: 'text', role: 'assistant', body: 'current'});
assert.equal(app.history.timeline.items[0].text, 'current');
instances[1].readyState = 0;
instances[1].emit('error', {});
assert.equal(reloaded.length, 0, 'transient disconnect keeps automatic resume');
instances[1].readyState = 2;
instances[1].emit('error', {});
assert.deepEqual(reloaded, ['session-b'], 'terminal stream error reloads the bounded page');
const indicator = {};
const usageContext = vm.createContext({$: () => indicator});
const usageStart = source.indexOf('function compactTokenCount(');
const usageEnd = source.indexOf('async function action(', usageStart);
vm.runInContext(source.slice(usageStart, usageEnd), usageContext);
vm.runInContext('renderContextUsage({usedTokens:404508, contextWindow:0})', usageContext);
assert.equal(indicator.hidden, false);
assert.match(indicator.textContent, /405k tokens/);
vm.runInContext('renderContextUsage({usedTokens:404508, contextWindow:200000, windowEstimated:true})', usageContext);
assert.doesNotMatch(indicator.textContent, /%/);
vm.runInContext('renderContextUsage({usedTokens:400000, contextWindow:1000000, windowEstimated:false})', usageContext);
assert.match(indicator.textContent, /40%/);
assert.ok(rendered.chat >= 3);
console.log('Session journal browser behavior: stream identity, paging cursor, upserts, roles, context, and switching pass.');
