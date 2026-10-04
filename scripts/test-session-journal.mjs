import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../internal/daemon/dashboard/app.js', import.meta.url), 'utf8');
const problemElements = {panel: {}, list: {}};
const problemApp = {state: {sessions: [{id: 'target', name: 'Worker'}], deliveryProblems: [{deliveryId: 19, sessionId: 'target', message: '<script>payload</script>', reason: 'Target gone', status: 'failed', at: '2026-10-04T00:00:00Z'}]}, selected: null};
const problemContext = vm.createContext({app: problemApp, $: selector => selector.endsWith('panel') ? problemElements.panel : problemElements.list, displayName: session => session.name, escape: value => String(value).replaceAll('<', '&lt;').replaceAll('>', '&gt;'), timeAgo: () => 'just now'});
const problemsStart = source.indexOf('function renderDeliveryProblems()');
const problemsEnd = source.indexOf('function sessionIsCurrent(', problemsStart);
vm.runInContext(source.slice(problemsStart, problemsEnd), problemContext);
vm.runInContext('renderDeliveryProblems()', problemContext);
assert.equal(problemElements.panel.hidden, false);
assert.ok(problemElements.list.innerHTML.includes('data-delivery-dismiss="19"'));
assert.ok(problemElements.list.innerHTML.includes('&lt;script&gt;'));
assert.ok(!problemElements.list.innerHTML.includes('data-retry'), 'delivery problems never offer automatic retry');
problemApp.state.deliveryProblems = [];
vm.runInContext('renderDeliveryProblems()', problemContext);
assert.equal(problemElements.panel.hidden, true);
const actionStart = source.indexOf('async function action(');
const actionEnd = source.indexOf('async function voiceRequest(', actionStart);
let dismissedRequest;
problemContext.fetch = async (url, options) => { dismissedRequest = {url, body: JSON.parse(options.body)}; return {ok: true, json: async () => ({ok: true})}; };
vm.runInContext(source.slice(actionStart, actionEnd), problemContext);
await vm.runInContext('action("delivery-dismiss", {deliveryId: 19})', problemContext);
assert.equal(dismissedRequest.body.deliveryId, 19);
assert.equal(dismissedRequest.body.action, 'delivery-dismiss');
const metadataStart = source.indexOf('function applySessionMetadata(');
const metadataEnd = source.indexOf('function renderChat(', metadataStart);
vm.runInContext(source.slice(metadataStart, metadataEnd), problemContext);
problemContext.detail = {journalSeq: 8, goal: {status: 'complete'}, context: {usedTokens: 42}};
vm.runInContext('applySessionMetadata(detail, {model: "new", goal: {status: "active"}, context: {usedTokens: 1}}, 7)', problemContext);
assert.equal(problemContext.detail.goal.status, 'complete', 'late metadata cannot overwrite streamed goal');
assert.equal(problemContext.detail.context.usedTokens, 42);
assert.equal(problemContext.detail.model, 'new');
vm.runInContext('applySessionMetadata(detail, {goal: null}, 8)', problemContext);
assert.equal(problemContext.detail.goal, null);
problemContext.imageItem = {kind: 'attachment', text: 'Image attachment', attachment: {id: 'image/1?x=<tag>', mediaType: 'image/png'}};
problemContext.imageSession = {id: 'session&1'};
const imageHTML = vm.runInContext('renderImageAttachment(imageItem, imageSession)', problemContext);
assert.ok(imageHTML.includes('/api/session-attachment?sessionId=session%261&id=image%2F1%3Fx%3D%3Ctag%3E'));
assert.ok(imageHTML.includes('loading="lazy"'));
problemContext.imageItem.attachment.mediaType = 'image/svg+xml';
assert.ok(!vm.runInContext('renderImageAttachment(imageItem, imageSession)', problemContext).includes('<img'), 'active SVG is not embedded');
problemContext.handoffMessage = text => ({text, label: 'You'});
problemContext.labels = {claude: 'Claude'};
problemContext.renderMessage = (text, role) => `<p data-role="${role}">${text}</p>`;
problemContext.imageSession.surface = 'claude';
problemContext.imageItem.attachment.mediaType = 'image/png';
problemContext.imageItems = [
  {id: 'u1', kind: 'message', role: 'user', text: 'Before image'},
  problemContext.imageItem,
  {id: 'a1', kind: 'message', role: 'assistant', text: 'After image'},
];
const timelineHTML = vm.runInContext('renderImageTimeline(imageItems, imageSession)', problemContext);
assert.ok(timelineHTML.indexOf('Before image') < timelineHTML.indexOf('<img'));
assert.ok(timelineHTML.indexOf('<img') < timelineHTML.indexOf('After image'));
assert.ok(timelineHTML.includes('data-role="user"') && timelineHTML.includes('data-role="agent"'));
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
instances[0].emit('item', {itemId: 'image-1', op: 'upsert', kind: 'attachment', role: 'user', body: 'Image attachment', attachment: {id: 'image-1', mediaType: 'image/png'}, callId: 'tool-1'});
assert.equal(app.history.timeline.items[2].attachment.id, 'image-1');
assert.equal(app.history.timeline.items[2].callId, 'tool-1');
assert.equal(app.history.exchanges.length, 1, 'attachments remain timeline items, not duplicate text exchanges');

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
const launcherSelect = {value: 'tmux', innerHTML: ''};
const launcherDetail = {};
const launcherAgent = {value: 'claude'};
const launcherContext = vm.createContext({app: {launchers: [
  {id: 'tmux', label: 'tmux', agents: ['claude', 'codex'], available: true, detail: 'Terminal session'},
  {id: 'cmux', label: '<cmux>', agents: ['claude', 'codex'], available: false, detail: 'Not installed'},
  {id: 'codex-app-server', label: 'Codex', agents: ['codex'], available: true, detail: ''},
]}, $: selector => selector.endsWith('detail') ? launcherDetail : selector.endsWith('surface') ? launcherAgent : launcherSelect,
escape: value => String(value).replaceAll('<', '&lt;').replaceAll('>', '&gt;')});
const launcherStart = source.indexOf('function renderStartLaunchers()');
const launcherEnd = source.indexOf('async function loadStartLaunchers()', launcherStart);
vm.runInContext(source.slice(launcherStart, launcherEnd), launcherContext);
vm.runInContext('renderStartLaunchers()', launcherContext);
assert.equal(launcherSelect.value, 'tmux');
assert.equal(launcherDetail.textContent, 'Terminal session');
assert.ok(launcherSelect.innerHTML.includes('value="cmux" disabled'));
assert.ok(launcherSelect.innerHTML.includes('&lt;cmux&gt;'));
assert.ok(!launcherSelect.innerHTML.includes('codex-app-server'));
launcherSelect.value = 'cmux';
vm.runInContext('renderStartLaunchers()', launcherContext);
assert.equal(launcherSelect.value, '', 'an unavailable launcher cannot remain selected');
console.log('Session journal browser behavior: stream identity, paging cursor, upserts, roles, context, and switching pass.');
