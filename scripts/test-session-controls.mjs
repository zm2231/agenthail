import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('../internal/daemon/dashboard/app.js', import.meta.url), 'utf8');
const options = ['session-fork', 'native-list', 'native-add', 'native-update', 'native-delete', 'native-reorder', 'native-start', 'session-lifecycle-status', 'session-lifecycle-logs', 'session-lifecycle-stop', 'session-lifecycle-resume'].map(value => ({value}));
const operation = {value: 'native-add', options, addEventListener() {}};
Object.defineProperty(operation, 'selectedOptions', {get: () => options.filter(x => x.value === operation.value)});
const elements = {operation};
for (const name of ['message', 'id', 'clientId', 'ids', 'cursor', 'cwd']) {
  const label = {};
  elements[name] = {name, value: '', closest: () => label};
}
const details = {};
const button = {};
let submit;
const form = {elements, closest: () => details, querySelector: () => button, querySelectorAll: () => Object.values(elements).filter(x => x.name), addEventListener: (_, handler) => {submit = handler;}};
const fields = {'#session-operation-form': form, '#session-operation-result': {textContent: ''}};
for (const name of ['effort', 'mode', 'tier', 'schema']) fields[`#turn-${name}`] = {value: '', closest: () => ({})};
const app = {selected: {id: 'source', surface: 'codex'}};
let fail = false;
let calls = [];
let next = 0;
const context = vm.createContext({
  app, $: selector => fields[selector], crypto: {randomUUID: () => `id-${++next}`},
  FormData: class {constructor(form) {return Object.entries(form.elements).filter(([,x]) => !x.disabled).map(([k,x]) => [k,x.value]);}},
  action: async (action, payload) => {calls.push({action,payload}); if (fail) throw Error('outcome unknown'); return {result:{data:[]}};},
  load: async () => {}, toast: () => {}, friendlyError: x => x.message,
});
vm.runInContext(source.slice(source.indexOf('function selectedTurnOptions()')), context);
const initialID = elements.clientId.value;
assert.ok(initialID);
elements.message.value = 'First input';
await submit({preventDefault(){}, currentTarget:form});
assert.equal(calls[0].payload.nativeQueue.clientUserMessageId, initialID);
assert.notEqual(elements.clientId.value, initialID, 'new work needs a fresh identity');
const retryID = elements.clientId.value;
fail = true;
await submit({preventDefault(){}, currentTarget:form});
await submit({preventDefault(){}, currentTarget:form});
assert.equal(elements.clientId.value, retryID);
assert.equal(calls[1].payload.nativeQueue.clientUserMessageId, calls[2].payload.nativeQueue.clientUserMessageId);
assert.equal(button.disabled, false);
app.selected.surface = 'claude';
vm.runInContext('syncSessionOperationFields()', context);
assert.equal(operation.value, 'session-lifecycle-status');
assert.equal(elements.message.disabled, true);
assert.equal(options.find(x => x.value === 'session-fork').disabled, true);
app.selected = {id:'readonly',surface:'codex',readOnly:true};
vm.runInContext('syncSessionOperationFields()', context);
assert.equal(operation.value, 'native-list');
assert.equal(elements.cursor.disabled, false);
app.selected.surface = 'notion';
vm.runInContext('syncSessionOperationFields()', context);
assert.equal(details.hidden, true);
console.log('Session controls: success rotates IDs, uncertain retries retain IDs, surface/read-only controls pass.');
