#if DEBUG && targetEnvironment(simulator)
import Foundation
import WebKit

enum VoiceEvaluation {
    static var enabled: Bool { ProcessInfo.processInfo.arguments.contains("--voice-evaluation") }
    static var directory: URL { URL.documentsDirectory.appendingPathComponent("VoiceEvaluation", isDirectory: true) }

    static var script: String {
        let recordings = ["request-1.wav", "request-2.wav"].compactMap { name in
            try? Data(contentsOf: directory.appendingPathComponent(name)).base64EncodedString()
        }
        guard recordings.count == 2, let json = try? JSONEncoder().encode(recordings), let values = String(data: json, encoding: .utf8) else {
            return "window.agenthailEvaluationReady=()=>false;"
        }
        return """
        (()=>{
          const recordings=\(values);let context,destination,next=0,recorder,chunks=[],peer;
          const report=value=>window.webkit.messageHandlers.voice.postMessage({type:'evaluationDiagnostic',value:JSON.stringify(value)});
          window.addEventListener('unhandledrejection',event=>report({error:String(event.reason)}));
          window.addEventListener('error',event=>report({error:event.message}));
          setInterval(async()=>{if(!peer)return;const stats=await peer.getStats();report({context:context?.state,time:context?.currentTime,next,stats:[...stats.values()].filter(s=>['outbound-rtp','inbound-rtp','media-source'].includes(s.type))});},5000);
          window.agenthailEvaluationSpeak=async()=>{
            if(next>=recordings.length)throw Error('No more simulated user requests');
            const bytes=Uint8Array.from(atob(recordings[next++]),c=>c.charCodeAt(0));
            const source=context.createBufferSource();source.buffer=await context.decodeAudioData(bytes.buffer);
            source.connect(destination);source.start();report({playing:next,duration:source.buffer.duration,context:context.state});
          };
          const capture=async()=>{
            context=new AudioContext();await context.resume();destination=context.createMediaStreamDestination();next=0;
            const silence=context.createConstantSource();silence.offset.value=0;silence.connect(destination);silence.start();
            return destination.stream;
          };
          const devices=navigator.mediaDevices;
          Object.defineProperty(devices,'getUserMedia',{value:capture});
          window.agenthailEvaluationReady=()=>navigator.mediaDevices===devices&&devices.getUserMedia===capture;
          const Peer=window.RTCPeerConnection;
          window.RTCPeerConnection=class extends Peer {
            constructor(...args){super(...args);peer=this;this.addEventListener('track',event=>{
              const mix=context.createMediaStreamDestination();context.createMediaStreamSource(event.streams[0]).connect(mix);
              context.createMediaStreamSource(destination.stream).connect(mix);chunks=[];recorder=new MediaRecorder(mix.stream);
              recorder.ondataavailable=e=>chunks.push(e.data);recorder.start(1000);
            });}
            createDataChannel(...args){const channel=super.createDataChannel(...args);channel.addEventListener('open',()=>{if(next===0)window.agenthailEvaluationSpeak();});return channel;}
          };
          window.agenthailEvaluationRecord=()=>{
            const current=recorder,recorded=chunks;recorder=undefined;peer=undefined;
            if(context&&context.state!=='closed')context.close();
            if(!current||current.state==='inactive')return;
            current.onstop=()=>{const reader=new FileReader();reader.onload=()=>window.webkit.messageHandlers.voice.postMessage({type:'evaluationRecording',value:reader.result});reader.readAsDataURL(new Blob(recorded,{type:current.mimeType}));};
            current.stop();
          };
        })();
        """
    }

    static func saveRecording(_ value: String) {
        guard enabled, let separator = value.firstIndex(of: ","), let data = Data(base64Encoded: String(value[value.index(after: separator)...])) else { return }
        let extensionName = value.hasPrefix("data:audio/webm") ? "webm" : "mp4"
        try? data.write(to: directory.appendingPathComponent("real-codex-dialogue.\(extensionName)"), options: .atomic)
    }

    static func saveDiagnostic(_ value: String) {
        guard enabled else { return }
        let url = directory.appendingPathComponent("diagnostics.jsonl")
        let data = Data((value + "\n").utf8)
        if !FileManager.default.fileExists(atPath: url.path) { try? data.write(to: url); return }
        guard let file = try? FileHandle(forWritingTo: url) else { return }
        defer { try? file.close() }
        _ = try? file.seekToEnd()
        try? file.write(contentsOf: data)
    }
}
#endif
