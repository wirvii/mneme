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
type Request struct {
	Token    string  `json:"token"`
	Action   string  `json:"action"`
	Engine   string  `json:"engine,omitempty"`
	Text     string  `json:"text,omitempty"`
	Language string  `json:"language,omitempty"`
	Voice    string  `json:"voice,omitempty"`
	Rate     float64 `json:"rate,omitempty"`
	Model    string  `json:"model,omitempty"`
	Launcher string  `json:"launcher,omitempty"`
}

// Response reports command success without echoing spoken text.
type Response struct {
	OK       bool   `json:"ok"`
	Speaking bool   `json:"speaking,omitempty"`
	Engine   string `json:"engine,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RuntimeDir returns the private speech state directory below dataDir.
func RuntimeDir(dataDir string) string { return filepath.Join(dataDir, "speech") }

// LauncherName returns the platform-native managed launcher filename.
func LauncherName(goos string) string {
	if goos == "windows" {
		return "launcher.exe"
	}
	return "launcher"
}

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

	s := &supervisor{token: desc.Token, done: make(chan struct{}), synth: speak, engine: engineName(runtimeGOOS)}
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
		return Response{OK: true, Speaking: s.speaking, Engine: s.engine, Error: s.lastError}
	case "stop":
		s.stopLocked()
		return Response{OK: true}
	case "shutdown":
		s.stopLocked()
		s.once.Do(func() { close(s.done) })
		return Response{OK: true}
	case "speak":
		s.stopLocked()
		ctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.speaking = true
		s.generation++
		generation := s.generation
		go func() {
			err := s.synth(ctx, req)
			s.mu.Lock()
			if s.generation == generation {
				s.cancel = nil
				s.speaking = false
				if err != nil && !errors.Is(err, context.Canceled) {
					s.lastError = "engine_failed"
				} else {
					s.lastError = ""
				}
			}
			s.mu.Unlock()
		}()
		return Response{OK: true, Speaking: true, Engine: s.engine}
	default:
		return Response{Error: "unknown action"}
	}
}

func (s *supervisor) stopLocked() {
	s.generation++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.speaking = false
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
	if req.Engine == "kokoro" {
		if err := speakKokoro(ctx, req); err == nil {
			return nil
		}
		req.Engine = ""
		req.Voice = ""
	}
	return speakForOS(ctx, runtimeGOOS, req)
}

func speakKokoro(ctx context.Context, req Request) error {
	if req.Launcher == "" || !filepath.IsAbs(req.Launcher) {
		return errors.New("speech: managed Kokoro launcher is unavailable")
	}
	launcherInfo, err := os.Stat(req.Launcher)
	if err != nil || launcherInfo.IsDir() {
		return errors.New("speech: managed Kokoro launcher is unavailable")
	}
	payload := struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Voice    string  `json:"voice"`
		Rate     float64 `json:"rate"`
		Model    string  `json:"model"`
	}{Text: req.Text, Language: req.Language, Voice: req.Voice, Rate: req.Rate, Model: req.Model}
	cmd := commandContext(ctx, req.Launcher)
	cmd.Env = append(os.Environ(), "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "NO_PROXY=*")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("speech: open Kokoro stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("speech: start Kokoro: %w", err)
	}
	encodeErr := json.NewEncoder(stdin).Encode(payload)
	closeErr := stdin.Close()
	if encodeErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("speech: send Kokoro request: %w", encodeErr)
	}
	if closeErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("speech: close Kokoro stdin: %w", closeErr)
	}
	if err := cmd.Wait(); err != nil {
		return errors.New("speech: Kokoro synthesis failed")
	}
	return nil
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
