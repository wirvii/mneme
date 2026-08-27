package speech

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const idleTimeout = 10 * time.Minute

var (
	commandContext    = exec.CommandContext
	lookPath          = exec.LookPath
	supervisorStarter = startSupervisorProcess
	runtimeGOOS       = runtime.GOOS
)

// RuntimeDescriptor lets short-lived clients find the private supervisor.
type RuntimeDescriptor struct {
	Address   string    `json:"address"`
	Token     string    `json:"token"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// Request is one authenticated supervisor command. Text is never persisted.
// SessionID identifies the emission's owner (empty for a client older than
// SPEC-129, which never queues under a session and never gets a spoken
// origin prefix). Origin is the spoken project label used to prefix the
// text when it plays while another session has focus (D10).
type Request struct {
	Token     string  `json:"token"`
	Action    string  `json:"action"`
	Text      string  `json:"text,omitempty"`
	Language  string  `json:"language,omitempty"`
	Voice     string  `json:"voice,omitempty"`
	Rate      float64 `json:"rate,omitempty"`
	Model     string  `json:"model,omitempty"`
	SessionID string  `json:"session_id,omitempty"`
	Origin    string  `json:"origin,omitempty"`
}

// Response reports command success without echoing spoken text. Speaking
// keeps its pre-SPEC-129 meaning: something is playing right now, of any
// session. Started reports whether THIS emission is the one playing;
// Position is its 1-based place in the queue while it waits (0 once it
// starts). Queue is only populated by the status action.
type Response struct {
	OK       bool        `json:"ok"`
	Speaking bool        `json:"speaking,omitempty"`
	Engine   string      `json:"engine,omitempty"`
	Error    string      `json:"error,omitempty"`
	Started  bool        `json:"started,omitempty"`
	Position int         `json:"position,omitempty"`
	Queue    *QueueStats `json:"queue,omitempty"`
}

// QueueStats are the audio-path counters the supervisor keeps in memory.
// They report since the supervisor started, not a lifetime total: a fresh,
// low number after a restart tells the truth about whether the channel is
// working now, which matters more than an eternal accumulator that never
// resets (D16).
type QueueStats struct {
	Pending             int       `json:"pending"`
	Started             int       `json:"started"`
	DroppedExpired      int       `json:"dropped_expired"`
	DroppedOverflow     int       `json:"dropped_overflow"`
	CancelledByPrompt   int       `json:"cancelled_by_prompt"`
	SupervisorStartedAt time.Time `json:"supervisor_started_at"`
}

// MaxQueuedEmissions is the maximum number of emissions waiting their turn,
// not counting the one currently playing. With EmissionTTL at two minutes,
// eight pending emissions are at most about a minute of audio, all of it
// recent.
const MaxQueuedEmissions = 8

// EmissionTTL is how long a queued emission may wait before it is discarded
// silently, measured from the moment it was enqueued.
const EmissionTTL = 2 * time.Minute

// RuntimeDir returns the private speech state directory below dataDir.
func RuntimeDir(dataDir string) string { return filepath.Join(dataDir, "speech") }

// Send submits a command to a running supervisor.
func Send(ctx context.Context, dataDir string, req Request) (Response, error) {
	desc, err := readDescriptor(dataDir)
	if err != nil {
		return Response{}, err
	}
	req.Token = desc.Token
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", desc.Address)
	if err != nil {
		return Response{}, fmt.Errorf("speech: connect supervisor: %w", err)
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("speech: send command: %w", err)
	}
	var response Response
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return Response{}, fmt.Errorf("speech: read response: %w", err)
	}
	if !response.OK {
		return response, errors.New(response.Error)
	}
	return response, nil
}

// EnsureSupervisor returns an existing healthy supervisor or launches one
// through the current mneme executable. Startup uses an atomic directory lock.
func EnsureSupervisor(ctx context.Context, dataDir string) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	if _, err := Send(ctx, dataDir, Request{Action: "status"}); err == nil {
		return nil
	}
	dir := RuntimeDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("speech: create runtime dir: %w", err)
	}
	lock := filepath.Join(dir, "startup.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("speech: acquire startup lock: %w", err)
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 15*time.Second {
			_ = os.Remove(lock)
			return EnsureSupervisor(ctx, dataDir)
		}
		return waitForSupervisor(ctx, dataDir)
	}
	defer os.Remove(lock)
	if err := supervisorStarter(ctx, dataDir); err != nil {
		return err
	}
	return waitForSupervisor(ctx, dataDir)
}

func startSupervisorProcess(_ context.Context, dataDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("speech: locate executable: %w", err)
	}
	cmd := exec.Command(exe, "--data-dir", dataDir, "speech", "supervise")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("speech: start supervisor: %w", err)
	}
	return nil
}

func waitForSupervisor(ctx context.Context, dataDir string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := Send(ctx, dataDir, Request{Action: "status"}); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("speech: supervisor startup: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func descriptorPath(dataDir string) string { return filepath.Join(RuntimeDir(dataDir), "runtime.json") }

// focusPath is where RegisterPrompt records the session the user last wrote
// in (D9).
func focusPath(dataDir string) string { return filepath.Join(RuntimeDir(dataDir), "focus.json") }

// readFocus returns the session_id last recorded in focus.json, or "" when
// dataDir is empty, the file is absent, or it cannot be parsed — an absent
// focus means no session has claimed it yet (typically a supervisor that
// just started for this very emission), which correctly yields no spoken
// origin prefix (D9/D10).
func readFocus(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	data, err := os.ReadFile(focusPath(dataDir))
	if err != nil {
		return ""
	}
	var focus struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &focus); err != nil {
		return ""
	}
	return focus.SessionID
}

func readDescriptor(dataDir string) (RuntimeDescriptor, error) {
	data, err := os.ReadFile(descriptorPath(dataDir))
	if err != nil {
		return RuntimeDescriptor{}, fmt.Errorf("speech: read runtime descriptor: %w", err)
	}
	var desc RuntimeDescriptor
	if err := json.Unmarshal(data, &desc); err != nil {
		return RuntimeDescriptor{}, fmt.Errorf("speech: parse runtime descriptor: %w", err)
	}
	if desc.Address == "" || desc.Token == "" {
		return RuntimeDescriptor{}, errors.New("speech: invalid runtime descriptor")
	}
	return desc, nil
}

// Supervise serves the authenticated loopback protocol until idle or shutdown.
func Supervise(ctx context.Context, dataDir string) error {
	dir := RuntimeDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("speech: create runtime dir: %w", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("speech: listen: %w", err)
	}
	defer func() { _ = listener.Close() }()
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("speech: generate token: %w", err)
	}
	desc := RuntimeDescriptor{Address: listener.Addr().String(), Token: hex.EncodeToString(tokenBytes), PID: os.Getpid(), StartedAt: time.Now().UTC()}
	data, _ := json.Marshal(desc)
	tmp := descriptorPath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("speech: write runtime descriptor: %w", err)
	}
	if err := os.Rename(tmp, descriptorPath(dataDir)); err != nil {
		return fmt.Errorf("speech: publish runtime descriptor: %w", err)
	}
	defer os.Remove(descriptorPath(dataDir))

	s := &supervisor{token: desc.Token, done: make(chan struct{}), synth: speak, engine: engineName(runtimeGOOS), dataDir: dataDir, startedAt: time.Now().UTC()}
	go func() {
		select {
		case <-ctx.Done():
		case <-s.done:
		}
		_ = listener.Close()
	}()
	for {
		if tcp, ok := listener.(*net.TCPListener); ok {
			_ = tcp.SetDeadline(time.Now().Add(idleTimeout))
		}
		conn, err := listener.Accept()
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				return nil
			}
			if ctx.Err() != nil || s.isDone() {
				return nil
			}
			return fmt.Errorf("speech: accept: %w", err)
		}
		go s.handle(conn)
	}
}

// queuedEmission is one emission waiting its turn. The text lives here, in
// memory, and nowhere else: it is never written to disk (AC12).
type queuedEmission struct {
	seq       uint64
	sessionID string // owner; empty means a client older than SPEC-129
	origin    string // spoken project label; may be empty
	req       Request
	enqueued  time.Time
}

type supervisor struct {
	mu         sync.Mutex
	token      string
	cancel     context.CancelFunc
	speaking   bool
	generation uint64
	done       chan struct{}
	once       sync.Once
	synth      func(context.Context, Request) error
	engine     string
	lastError  string

	// Queue-with-owner state (SPEC-129). current/queue/seq track the cola;
	// dataDir lets startLocked read focus.json; now is an injectable clock
	// so caducidad tests never need time.Sleep; startedAt/stats back the
	// QueueStats a status response reports.
	current   *queuedEmission
	queue     []*queuedEmission
	seq       uint64
	dataDir   string
	now       func() time.Time
	startedAt time.Time
	stats     QueueStats
}

// nowFn returns the injected clock when the test sets one, or time.Now
// otherwise. It is a method, not a package variable, because existing tests
// already build &supervisor{...} by hand (see TestSupervisorActionsReplaceAndStop).
func (s *supervisor) nowFn() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *supervisor) isDone() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *supervisor) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, 64*1024)).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(Response{Error: "invalid request"})
		return
	}
	if req.Token != s.token {
		_ = json.NewEncoder(conn).Encode(Response{Error: "unauthorized"})
		return
	}
	response := s.execute(req)
	_ = json.NewEncoder(conn).Encode(response)
}

func (s *supervisor) execute(req Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch req.Action {
	case "status":
		return Response{OK: true, Speaking: s.speaking, Engine: s.engine, Error: s.lastError, Queue: s.queueStatsLocked()}
	case "stop":
		// A host-scoped "be quiet" from the person: cancels whatever is
		// playing and empties the queue entirely. Discards here are NOT
		// counted (D8) — this is an explicit request, not a loss.
		s.stopLocked()
		return Response{OK: true}
	case "shutdown":
		s.stopLocked()
		s.once.Do(func() { close(s.done) })
		return Response{OK: true}
	case "speak":
		return s.enqueueLocked(req)
	case "cancel":
		return s.cancelSessionLocked(req)
	default:
		return Response{Error: "unknown action"}
	}
}

// queueStatsLocked snapshots the audio-path counters for a status response.
// Must be called with s.mu held.
func (s *supervisor) queueStatsLocked() *QueueStats {
	stats := s.stats
	stats.Pending = len(s.queue)
	stats.SupervisorStartedAt = s.startedAt
	return &stats
}

// enqueueLocked appends req to the queue under its session's ownership and
// pumps the queue once. It never cancels whatever is already playing —
// that is the entire point of D6/D7: writing a new emission no longer
// silences the machine, only the sessions cancel does. Must be called with
// s.mu held.
func (s *supervisor) enqueueLocked(req Request) Response {
	s.seq++
	entry := &queuedEmission{seq: s.seq, sessionID: req.SessionID, origin: req.Origin, req: req, enqueued: s.nowFn()}
	if len(s.queue) >= MaxQueuedEmissions {
		// Overflow evicts the oldest PENDING entry, never the one playing:
		// current lives outside s.queue (D13).
		s.queue = s.queue[1:]
		s.stats.DroppedOverflow++
	}
	s.queue = append(s.queue, entry)
	s.pumpLocked()
	if s.current == entry {
		return Response{OK: true, Speaking: true, Started: true, Engine: s.engine}
	}
	position := 0
	for i, queued := range s.queue {
		if queued == entry {
			position = i + 1
			break
		}
	}
	return Response{OK: true, Speaking: s.speaking, Started: false, Position: position, Engine: s.engine}
}

// cancelSessionLocked implements the queue-with-owner cancellation rule
// (D7/D8): writing in a session cancels ALL of that session's audio — what
// is playing and what is waiting — and touches nothing that belongs to
// another session. Must be called with s.mu held.
func (s *supervisor) cancelSessionLocked(req Request) Response {
	if req.SessionID == "" {
		return Response{Error: "cancel requires session_id"}
	}
	if s.current != nil && s.current.sessionID == req.SessionID {
		s.cancelCurrentLocked()
		s.stats.CancelledByPrompt++
	}
	s.stats.CancelledByPrompt += s.dropSessionLocked(req.SessionID)
	s.pumpLocked()
	return Response{OK: true}
}

// pumpLocked starts the next queued emission when nothing is playing,
// discarding any entry that has been waiting longer than EmissionTTL along
// the way (silently: no summary, no text produced — D13). Must be called
// with s.mu held.
func (s *supervisor) pumpLocked() {
	if s.speaking {
		return
	}
	for len(s.queue) > 0 {
		entry := s.queue[0]
		s.queue = s.queue[1:]
		if s.nowFn().Sub(entry.enqueued) > EmissionTTL {
			s.stats.DroppedExpired++
			continue
		}
		s.startLocked(entry)
		return
	}
}

// startLocked begins synthesizing entry. It decides the spoken origin
// prefix here, at the moment playback actually starts, never at enqueue
// time — an emission can sit in the queue while the user switches windows,
// and deciding earlier would make the prefix lie (D9). Must be called with
// s.mu held.
func (s *supervisor) startLocked(entry *queuedEmission) {
	req := entry.req
	focus := readFocus(s.dataDir)
	if focus != "" && entry.sessionID != "" && entry.sessionID != focus && entry.origin != "" {
		req.Text = OriginPrefix(req.Language, entry.origin) + req.Text
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.speaking = true
	s.current = entry
	s.generation++
	s.stats.Started++
	generation := s.generation
	go func() {
		err := s.synth(ctx, req)
		s.mu.Lock()
		if s.generation == generation {
			s.cancel = nil
			s.speaking = false
			s.current = nil
			if err != nil && !errors.Is(err, context.Canceled) {
				s.lastError = "engine_failed"
			} else {
				s.lastError = ""
			}
			s.pumpLocked()
		}
		s.mu.Unlock()
	}()
}

// cancelCurrentLocked stops whatever is playing without touching the
// queue — the mechanism `stop` and `cancel` both build on. Must be called
// with s.mu held.
func (s *supervisor) cancelCurrentLocked() {
	s.generation++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.speaking = false
	s.current = nil
}

// dropSessionLocked removes every queued entry owned by sessionID and
// reports how many were dropped. Must be called with s.mu held.
func (s *supervisor) dropSessionLocked(sessionID string) int {
	if sessionID == "" {
		return 0
	}
	kept := s.queue[:0]
	dropped := 0
	for _, entry := range s.queue {
		if entry.sessionID == sessionID {
			dropped++
			continue
		}
		kept = append(kept, entry)
	}
	s.queue = kept
	return dropped
}

// stopLocked cancels whatever is playing and empties the queue entirely —
// the host-scoped "be quiet" that `stop`/`shutdown` use. Must be called
// with s.mu held.
func (s *supervisor) stopLocked() {
	s.cancelCurrentLocked()
	s.queue = nil
}

func engineName(goos string) string {
	switch goos {
	case "darwin":
		return "say"
	case "windows":
		return "system.speech"
	case "linux":
		return "piper"
	default:
		return "unsupported"
	}
}

// EngineName returns the local engine selected for an operating system.
func EngineName(goos string) string { return engineName(goos) }

func speak(ctx context.Context, req Request) error {
	return speakForOS(ctx, runtimeGOOS, req)
}

func speakForOS(ctx context.Context, goos string, req Request) error {
	switch goos {
	case "darwin":
		args := []string{}
		if req.Voice != "" {
			args = append(args, "--voice", req.Voice)
		}
		if req.Rate > 0 {
			args = append(args, "--rate", strconv.Itoa(int(175*req.Rate)))
		}
		cmd := commandContext(ctx, "say", args...)
		cmd.Stdin = strings.NewReader(req.Text)
		return cmd.Run()
	case "windows":
		const script = `[Console]::InputEncoding=[Text.UTF8Encoding]::new($false); $text=[Console]::In.ReadToEnd(); Add-Type -AssemblyName System.Speech; $s=New-Object System.Speech.Synthesis.SpeechSynthesizer; if($env:MNEME_SPEECH_VOICE){$s.SelectVoice($env:MNEME_SPEECH_VOICE)}; $s.Rate=[Math]::Max(-10,[Math]::Min(10,[int]$env:MNEME_SPEECH_RATE)); $s.Speak($text)`
		cmd := commandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		cmd.Stdin = strings.NewReader(req.Text)
		cmd.Env = append(os.Environ(), "MNEME_SPEECH_VOICE="+req.Voice, "MNEME_SPEECH_RATE="+strconv.Itoa(int((req.Rate-1)*5)))
		return cmd.Run()
	case "linux":
		if req.Model == "" {
			return errors.New("speech: piper model is not configured")
		}
		piper := commandContext(ctx, "piper", "--model", req.Model, "--output-raw")
		playerName, playerArgs := linuxPlayer()
		if playerName == "" {
			return errors.New("speech: no local audio player found")
		}
		player := commandContext(ctx, playerName, playerArgs...)
		pipe, err := piper.StdoutPipe()
		if err != nil {
			return err
		}
		player.Stdin = pipe
		piper.Stdin = strings.NewReader(req.Text)
		if err := player.Start(); err != nil {
			return err
		}
		if err := piper.Run(); err != nil {
			_ = player.Process.Kill()
			return err
		}
		return player.Wait()
	default:
		return fmt.Errorf("speech: unsupported operating system %s", goos)
	}
}

func linuxPlayer() (string, []string) {
	if _, err := lookPath("aplay"); err == nil {
		return "aplay", []string{"-q", "-r", "22050", "-f", "S16_LE", "-t", "raw", "-"}
	}
	if _, err := lookPath("paplay"); err == nil {
		return "paplay", []string{"--raw", "--rate=22050", "--format=s16le", "-"}
	}
	if _, err := lookPath("ffplay"); err == nil {
		return "ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet", "-f", "s16le", "-ar", "22050", "-ac", "1", "-"}
	}
	return "", nil
}

// ListVoices returns locally installed system voices without network access.
func ListVoices(ctx context.Context) ([]string, error) {
	return listVoicesForOS(ctx, runtimeGOOS)
}

func listVoicesForOS(ctx context.Context, goos string) ([]string, error) {
	var cmd *exec.Cmd
	switch goos {
	case "darwin":
		cmd = commandContext(ctx, "say", "--voice", "?")
	case "windows":
		cmd = commandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `Add-Type -AssemblyName System.Speech; (New-Object System.Speech.Synthesis.SpeechSynthesizer).GetInstalledVoices() | ForEach-Object {$_.VoiceInfo.Name}`)
	case "linux":
		return nil, errors.New("speech: Linux voices are Piper model files; use speech setup")
	default:
		return nil, fmt.Errorf("speech: unsupported operating system %s", goos)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("speech: list voices: %w", err)
	}
	var voices []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if v := strings.TrimSpace(scanner.Text()); v != "" {
			voices = append(voices, v)
		}
	}
	return voices, scanner.Err()
}
