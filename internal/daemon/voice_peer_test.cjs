const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');

function fixture(capture, iceGatheringState = 'complete') {
  const events = [], peers = [], tracks = [];
  const audio = {srcObject: null, play: async () => {}};
  const track = {enabled:true, stopped:false, stop() { this.stopped = true; }};
  tracks.push(track);
  class Peer {
    constructor() { this.iceGatheringState = iceGatheringState; peers.push(this); }
    addTrack(track, stream) { this.track = track; this.stream = stream; }
    createDataChannel(name) { this.channel = {name, close() { this.closed = true; }}; return this.channel; }
    createOffer() { return Promise.resolve({type:'offer', sdp:'v=0 fixture-offer'}); }
    setLocalDescription(description) { this.localDescription = description; return Promise.resolve(); }
    setRemoteDescription(description) { this.remoteDescription = description; return Promise.resolve(); }
    close() { this.closed = true; }
  }
  const stream = {getTracks: () => tracks, getAudioTracks: () => tracks};
  const context = {
    window: {webkit:{messageHandlers:{voice:{postMessage: message => events.push(message)}}}, addEventListener() {}},
    document: {querySelector: () => audio},
    navigator: {mediaDevices: {getUserMedia: capture ?? (async options => { assert.equal(options.video,false); return stream; })}},
    RTCPeerConnection: Peer, setTimeout, clearTimeout
  };
  vm.runInNewContext(fs.readFileSync(__dirname + '/voice_peer.js', 'utf8'), context);
  return {api:context.window.agenthailVoice, events, peers, tracks, stream, audio};
}

test('actual peer program negotiates audio and applies the Codex answer', async () => {
  const f = fixture();
  await f.api.start();
  assert.deepEqual(f.events.map(x => x.type), ['ready','offer']);
  assert.equal(f.peers[0].channel.name, 'oai-events');
  assert.equal(f.peers[0].track, f.tracks[0]);
  await f.api.answer('v=0 fixture-answer');
  assert.equal(f.peers[0].remoteDescription.sdp, 'v=0 fixture-answer');
  f.api.mute(true); assert.equal(f.tracks[0].enabled,false);
  f.api.mute(false); assert.equal(f.tracks[0].enabled,true);
  f.api.end();
  assert.equal(f.tracks[0].stopped,true);
  assert.equal(f.peers[0].closed,true);
  assert.equal(f.peers[0].channel.closed,true);
  assert.equal(f.audio.srcObject,null);
});

test('offer is delivered without waiting for ICE gathering to complete', async () => {
  const f = fixture(undefined, 'gathering');
  await f.api.start();
  assert.deepEqual(f.events.map(x => x.type), ['ready', 'offer']);
  assert.equal(f.events.at(-1).value, 'v=0 fixture-offer');
});

test('hangup during microphone permission cannot revive the call', async () => {
  let allow;
  const f=fixture(() => new Promise(resolve => { allow=resolve; }));
  const pending=f.api.start();
  f.api.end();
  allow(f.stream); await pending;
  assert.equal(f.tracks[0].stopped,true);
  assert.equal(f.peers.length,0);
  assert.deepEqual(f.events.map(x => x.type), ['ready']);
});

test('permission denial is visible and creates no peer', async () => {
  const f=fixture(async () => { throw new Error('microphone denied'); });
  await f.api.start();
  assert.equal(f.peers.length,0);
  assert.equal(f.events.at(-1).type,'error');
  assert.equal(f.events.at(-1).value,'microphone denied');
});

test('an old peer cannot report connection after hangup', async () => {
  const f=fixture(); await f.api.start();
  const peer=f.peers[0]; f.api.end();
  const before=f.events.length;
  peer.connectionState='connected'; peer.onconnectionstatechange(); peer.channel.onopen();
  assert.equal(f.events.length,before);
});
