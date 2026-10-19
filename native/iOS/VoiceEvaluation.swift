#if DEBUG && targetEnvironment(simulator)
import AVFAudio
import Foundation
@preconcurrency import WebRTC

// Simulator evaluation: recorded user requests replace the microphone through a
// WebRTC audio device, and both sides of the real call are saved for review.
enum VoiceEvaluation {
    @MainActor static var enabled = ProcessInfo.processInfo.arguments.contains("--voice-evaluation")
    @MainActor static var directory = URL.documentsDirectory.appendingPathComponent("VoiceEvaluation", isDirectory: true)
    static let device = EvaluationAudioDevice()

    @MainActor static func prepare() throws {
        let requests = try ["request-1.wav", "request-2.wav"].map { try samples(directory.appendingPathComponent($0)) }
        device.load(requests: requests, output: directory)
    }

    static func samples(_ url: URL) throws -> [Int16] {
        let file = try AVAudioFile(forReading: url)
        guard let format = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: EvaluationAudioDevice.sampleRate, channels: 1, interleaved: true),
              let converter = AVAudioConverter(from: file.processingFormat, to: format),
              let input = AVAudioPCMBuffer(pcmFormat: file.processingFormat, frameCapacity: AVAudioFrameCount(file.length)) else {
            throw AgenthailAPIError.unavailable("Evaluation recording \(url.lastPathComponent) cannot be converted.")
        }
        try file.read(into: input)
        let capacity = AVAudioFrameCount(Double(input.frameLength) * EvaluationAudioDevice.sampleRate / file.processingFormat.sampleRate) + 1024
        guard let output = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: capacity) else { throw AgenthailAPIError.invalidResponse }
        var supplied = false
        var failure: NSError?
        converter.convert(to: output, error: &failure) { _, status in
            if supplied { status.pointee = .endOfStream; return nil }
            supplied = true
            status.pointee = .haveData
            return input
        }
        if let failure { throw failure }
        guard let data = output.int16ChannelData, output.frameLength > 0 else {
            throw AgenthailAPIError.unavailable("Evaluation recording \(url.lastPathComponent) is empty.")
        }
        return Array(UnsafeBufferPointer(start: data[0], count: Int(output.frameLength)))
    }
}

final class EvaluationAudioDevice: NSObject, RTCAudioDevice, @unchecked Sendable {
    static let sampleRate = 48_000.0
    private static let frames: UInt32 = 480
    private let lock = NSLock()
    private let queue = DispatchQueue(label: "systems.agenthail.voice-evaluation")
    private var delegate: (any RTCAudioDeviceDelegate)?
    private var timer: (any DispatchSourceTimer)?
    private var requests: [[Int16]] = []
    private var next = 0
    private var pending: ArraySlice<Int16> = []
    private var dialogue: [Int16] = []
    private var output: URL?
    private var sampleTime = 0.0
    private var inputEnergy = 0.0
    private var outputEnergy = 0.0
    private var lastReport = Date()
    private var initialized = false
    private var playoutInitialized = false
    private var recordingInitialized = false
    private var playing = false
    private var recording = false

    func load(requests: [[Int16]], output: URL) {
        lock.withLock {
            self.requests = requests
            self.output = output
            next = 0
            pending = []
            dialogue = []
        }
    }

    @discardableResult
    func speakNext() -> Bool {
        lock.withLock {
            guard next < requests.count else { return false }
            pending.append(contentsOf: requests[next])
            next += 1
            return true
        }
    }

    var hasNextRequest: Bool { lock.withLock { next < requests.count } }

    var deviceInputSampleRate: Double { Self.sampleRate }
    var inputIOBufferDuration: TimeInterval { Double(Self.frames) / Self.sampleRate }
    var inputNumberOfChannels: Int { 1 }
    var inputLatency: TimeInterval { 0 }
    var deviceOutputSampleRate: Double { Self.sampleRate }
    var outputIOBufferDuration: TimeInterval { Double(Self.frames) / Self.sampleRate }
    var outputNumberOfChannels: Int { 1 }
    var outputLatency: TimeInterval { 0 }
    var isInitialized: Bool { lock.withLock { initialized } }
    var isPlayoutInitialized: Bool { lock.withLock { playoutInitialized } }
    var isPlaying: Bool { lock.withLock { playing } }
    var isRecordingInitialized: Bool { lock.withLock { recordingInitialized } }
    var isRecording: Bool { lock.withLock { recording } }

    func initialize(with delegate: any RTCAudioDeviceDelegate) -> Bool {
        lock.withLock { self.delegate = delegate; initialized = true }
        return true
    }

    func terminateDevice() -> Bool {
        _ = stopPlayout()
        _ = stopRecording()
        lock.withLock { delegate = nil; initialized = false; playoutInitialized = false; recordingInitialized = false }
        return true
    }

    func initializePlayout() -> Bool { lock.withLock { playoutInitialized = true }; return true }
    func startPlayout() -> Bool { lock.withLock { playing = true }; updateTimer(); return true }
    func stopPlayout() -> Bool { lock.withLock { playing = false }; updateTimer(); return true }
    func initializeRecording() -> Bool { lock.withLock { recordingInitialized = true }; return true }
    func startRecording() -> Bool { lock.withLock { recording = true }; updateTimer(); return true }

    func stopRecording() -> Bool {
        lock.withLock { recording = false }
        updateTimer()
        saveDialogue()
        return true
    }

    private func updateTimer() {
        lock.lock()
        let running = playing || recording
        if running, timer == nil {
            let source = DispatchSource.makeTimerSource(queue: queue)
            source.schedule(deadline: .now(), repeating: .milliseconds(10), leeway: .milliseconds(1))
            source.setEventHandler { [weak self] in self?.tick() }
            timer = source
            lock.unlock()
            source.resume()
            return
        }
        let stopped = running ? nil : timer
        if !running { timer = nil }
        lock.unlock()
        stopped?.cancel()
    }

    private func tick() {
        let count = Int(Self.frames)
        lock.lock()
        guard let delegate else { lock.unlock(); return }
        let recording = recording, playing = playing
        var input = [Int16](repeating: 0, count: count)
        if recording {
            let taken = pending.prefix(count)
            input.replaceSubrange(0..<taken.count, with: taken)
            pending.removeFirst(taken.count)
        }
        var timestamp = AudioTimeStamp()
        timestamp.mSampleTime = sampleTime
        timestamp.mHostTime = mach_absolute_time()
        timestamp.mFlags = [.sampleTimeValid, .hostTimeValid]
        sampleTime += Double(count)
        lock.unlock()

        var flags = AudioUnitRenderActionFlags()
        if recording {
            input.withUnsafeMutableBytes { bytes in
                var list = AudioBufferList(mNumberBuffers: 1, mBuffers: AudioBuffer(mNumberChannels: 1, mDataByteSize: UInt32(bytes.count), mData: bytes.baseAddress))
                _ = delegate.deliverRecordedData(&flags, &timestamp, 1, Self.frames, &list, nil, nil)
            }
        }
        var received = [Int16](repeating: 0, count: count)
        if playing {
            received.withUnsafeMutableBytes { bytes in
                var list = AudioBufferList(mNumberBuffers: 1, mBuffers: AudioBuffer(mNumberChannels: 1, mDataByteSize: UInt32(bytes.count), mData: bytes.baseAddress))
                _ = delegate.getPlayoutData(&flags, &timestamp, 0, Self.frames, &list)
            }
        }
        lock.lock()
        for index in 0..<count {
            dialogue.append(Int16(clamping: Int(input[index]) + Int(received[index])))
            inputEnergy += Double(input[index]) * Double(input[index])
            outputEnergy += Double(received[index]) * Double(received[index])
        }
        let report = Date().timeIntervalSince(lastReport) >= 5
        let line = report ? diagnostic() : nil
        lock.unlock()
        if let line { append(line) }
    }

    private func diagnostic() -> String {
        lastReport = Date()
        let value: [String: Any] = [
            "at": ISO8601DateFormatter().string(from: lastReport), "requestsSent": next,
            "queuedSamples": pending.count, "inputEnergy": inputEnergy, "outputEnergy": outputEnergy,
            "recording": recording, "playing": playing,
        ]
        inputEnergy = 0; outputEnergy = 0
        let data = (try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])) ?? Data()
        return String(decoding: data, as: UTF8.self)
    }

    private func append(_ line: String) {
        guard let directory = lock.withLock({ output }) else { return }
        let url = directory.appendingPathComponent("diagnostics.jsonl")
        let data = Data((line + "\n").utf8)
        guard let file = try? FileHandle(forWritingTo: url) else { try? data.write(to: url); return }
        defer { try? file.close() }
        _ = try? file.seekToEnd()
        try? file.write(contentsOf: data)
    }

    private func saveDialogue() {
        let (samples, directory) = lock.withLock { () -> ([Int16], URL?) in
            let samples = dialogue
            dialogue = []
            return (samples, output)
        }
        guard let directory, !samples.isEmpty,
              let format = AVAudioFormat(commonFormat: .pcmFormatInt16, sampleRate: Self.sampleRate, channels: 1, interleaved: true),
              let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(samples.count)),
              let channel = buffer.int16ChannelData else { return }
        samples.withUnsafeBufferPointer { channel[0].update(from: $0.baseAddress!, count: samples.count) }
        buffer.frameLength = AVAudioFrameCount(samples.count)
        let url = directory.appendingPathComponent("real-codex-dialogue.wav")
        try? FileManager.default.removeItem(at: url)
        do {
            let file = try AVAudioFile(forWriting: url, settings: format.settings, commonFormat: .pcmFormatInt16, interleaved: true)
            try file.write(from: buffer)
        } catch {
            append(#"{"error":"dialogue recording could not be saved"}"#)
        }
    }
}
#endif
