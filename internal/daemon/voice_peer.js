(() => {
  let peer, stream, channel, generation = 0;
  const notify = (type, value = "") => window.webkit.messageHandlers.voice.postMessage({type, value});
  const audio = document.querySelector("audio");
  const end = () => {
    generation++;
    if (stream) stream.getTracks().forEach(track => track.stop());
    if (channel) channel.close();
    if (peer) peer.close();
    audio.pause();
    audio.srcObject = null;
    stream = peer = channel = null;
  };
  const start = async () => {
    end();
    const current = generation;
    try {
      const capture = await navigator.mediaDevices.getUserMedia({audio: {echoCancellation:true, noiseSuppression:true}, video:false});
      if (generation !== current) { capture.getTracks().forEach(track => track.stop()); return; }
      stream = capture;
      const connection = new RTCPeerConnection();
      peer = connection;
      stream.getAudioTracks().forEach(track => {
        track.onended = () => { if (generation === current) notify("error", "The microphone capture ended. Call again to resume."); };
        connection.addTrack(track, stream);
      });
      connection.ontrack = event => { if (generation === current) { audio.srcObject = event.streams[0]; audio.play().catch(error => notify("error", "Audio playback: " + error.message)); } };
      connection.onconnectionstatechange = () => { if (generation === current) notify("connection", connection.connectionState); };
      channel = connection.createDataChannel("oai-events");
      channel.onopen = () => { if (generation === current) notify("channel", "open"); };
      channel.onerror = () => { if (generation === current) notify("error", "Codex voice data channel failed"); };
      await connection.setLocalDescription(await connection.createOffer());
      if (generation === current) notify("offer", connection.localDescription.sdp);
    } catch (error) {
      if (generation === current) { end(); notify("error", error.message); }
    }
  };
  window.agenthailVoice = {
    start,
    end,
    mute: muted => { if (stream) stream.getAudioTracks().forEach(track => { track.enabled = !muted; }); },
    answer: async sdp => {
      try { if (!peer) throw new Error("No pending audio connection"); await peer.setRemoteDescription({type:"answer", sdp}); }
      catch (error) { end(); notify("error", error.message); }
    }
  };
  window.addEventListener("pagehide", end);
  notify("ready");
})();
