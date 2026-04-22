package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// helloTimeout bounds the startup handshake. A well-written
// plugin replies within tens of milliseconds; 5s is the
// generous ceiling beyond which we give up and mark the plugin
// as broken. Kept separate from the candidates-call timeout
// (the v0.6 spec put that at 2s) because startup costs can
// include JIT warmup, module loads, etc. that don't recur.
const helloTimeout = 5 * time.Second

// callTimeout bounds a single candidates call. Matches the
// v0.6 roadmap: a plugin that can't answer in 2s yields an
// empty result; the engine moves on.
const callTimeout = 2 * time.Second

// ErrPluginClosed is returned by Call after Close has been
// invoked. Indicates the child is no longer live; callers
// that see it should stop issuing requests.
var ErrPluginClosed = errors.New("plugin: closed")

// Process wraps one spawned plugin child: stdin/stdout pipes,
// the JSON-RPC id counter, an inflight-request map keyed by
// id, and a shutdown hook. Thread-safe for concurrent Call
// invocations — tests and the engine's parallel fan-out both
// rely on this.
type Process struct {
	dir  string // plugin directory path, for logging
	name string // display name (basename of dir) for logging

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	nextID   atomic.Uint64
	muCalls  sync.Mutex
	inflight map[uint64]chan Response

	closeOnce sync.Once
	closed    chan struct{}

	log *slog.Logger
}

// spawnProcess forks the executable at {dir}/bucket with stdin
// + stdout piped. Returns as soon as the process is alive; no
// handshake yet (that happens separately via Hello). Callers
// own the returned Process and must Close it at shutdown.
//
// Startup errors (executable not found, permission denied) are
// returned directly; the caller decides whether to retry or
// skip the plugin.
func spawnProcess(ctx context.Context, dir, name string, log *slog.Logger) (*Process, error) {
	exe := dir + "/bucket"
	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin %s: stdin pipe: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("plugin %s: stdout pipe: %w", name, err)
	}
	// stderr is inherited so plugin authors see their own log
	// output in muzika's journal. Alternative would be piping
	// to slog but that adds encoding constraints on the plugin
	// side; inheriting is simpler.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("plugin %s: start: %w", name, err)
	}

	p := &Process{
		dir:      dir,
		name:     name,
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		inflight: make(map[uint64]chan Response),
		closed:   make(chan struct{}),
		log:      log,
	}
	go p.readLoop()
	return p, nil
}

// readLoop drains the child's stdout, decoding one JSON object
// per line and dispatching to the waiting Call by id. Unknown
// ids are logged and dropped — a well-written plugin only emits
// replies to our requests, but we don't crash if it doesn't.
//
// Exits when stdout closes (child exited or pipe broken).
// Signals all waiting Calls by closing their response channels
// so they return ErrPluginClosed rather than blocking forever.
func (p *Process) readLoop() {
	defer p.drainInflight()
	scanner := bufio.NewScanner(p.stdout)
	// Plugin responses can include full artist discographies as
	// candidate lists. 1MB is generous but bounded; plugins that
	// blow past it are misbehaving and should chunk or paginate
	// (future protocol extension).
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			p.log.Warn("plugin: unparseable response",
				"plugin", p.name, "err", err)
			continue
		}
		p.muCalls.Lock()
		ch, ok := p.inflight[resp.ID]
		if ok {
			delete(p.inflight, resp.ID)
		}
		p.muCalls.Unlock()
		if !ok {
			p.log.Debug("plugin: response for unknown id",
				"plugin", p.name, "id", resp.ID)
			continue
		}
		// Non-blocking send: the waiter has a buffer=1 channel,
		// so a receiver that's already gone (ctx cancelled mid-
		// call) won't deadlock us. The response is dropped.
		select {
		case ch <- resp:
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		p.log.Debug("plugin: stdout read ended",
			"plugin", p.name, "err", err)
	}
}

// drainInflight fails every pending Call when readLoop exits.
// Protects against goroutine leaks on plugin crash or shutdown.
func (p *Process) drainInflight() {
	p.muCalls.Lock()
	defer p.muCalls.Unlock()
	for id, ch := range p.inflight {
		close(ch)
		delete(p.inflight, id)
	}
}

// Call issues one JSON-RPC request and waits for the matching
// response. Blocks until one of three things happens:
//
//   - response arrives: returned as-is. Plugin-side errors come
//     back in Response.Error; caller decides what to do.
//   - timeout: Call returns context.DeadlineExceeded. The request
//     id stays registered; if a late response arrives it's
//     matched and discarded (non-blocking send).
//   - plugin closes: Call returns ErrPluginClosed.
//
// Safe for concurrent use.
func (p *Process) Call(ctx context.Context, method string, params any, timeout time.Duration) (Response, error) {
	select {
	case <-p.closed:
		return Response{}, ErrPluginClosed
	default:
	}

	id := p.nextID.Add(1)
	var paramsRaw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return Response{}, fmt.Errorf("plugin %s: marshal params: %w", p.name, err)
		}
		paramsRaw = b
	}
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  paramsRaw,
	}
	line, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("plugin %s: marshal request: %w", p.name, err)
	}

	respCh := make(chan Response, 1)
	p.muCalls.Lock()
	p.inflight[id] = respCh
	p.muCalls.Unlock()

	// Serialize writes: simultaneous json.Marshal → Write from
	// two goroutines could interleave at the OS level. A single
	// Write per message is the protocol guarantee.
	if _, err := p.stdin.Write(append(line, '\n')); err != nil {
		p.muCalls.Lock()
		delete(p.inflight, id)
		p.muCalls.Unlock()
		return Response{}, fmt.Errorf("plugin %s: write: %w", p.name, err)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case resp, ok := <-respCh:
		if !ok {
			return Response{}, ErrPluginClosed
		}
		return resp, nil
	case <-callCtx.Done():
		return Response{}, callCtx.Err()
	}
}

// Close terminates the child process. Idempotent. Sends SIGTERM
// via ctx cancellation (the exec.CommandContext used at spawn
// handles that); waits briefly for a graceful exit; if the child
// doesn't exit within 2s, Kill forces it. Safe to call during
// shutdown of the whole service.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		close(p.closed)
		_ = p.stdin.Close()
		// Give the child a moment to exit cleanly after stdin EOF.
		// Most well-behaved plugins (the reference Go one, any
		// stdlib json-rpc script) will notice stdin EOF and
		// terminate. The kill is belt-and-suspenders for hangs.
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = p.cmd.Process.Kill()
			<-done
		}
	})
	return nil
}

// Hello runs the startup handshake. Call once after spawnProcess;
// the resulting HelloResult becomes the plugin's public Bucket
// metadata. Errors leave the plugin unregistered (the Manager
// logs + skips).
func (p *Process) Hello(ctx context.Context, muzikaVersion string) (HelloResult, error) {
	resp, err := p.Call(ctx, "hello", HelloParams{
		MuzikaVersion:   muzikaVersion,
		ProtocolVersion: ProtocolVersion,
	}, helloTimeout)
	if err != nil {
		return HelloResult{}, err
	}
	if resp.Error != nil {
		return HelloResult{}, fmt.Errorf("plugin %s: hello error: %s", p.name, resp.Error.Message)
	}
	var out HelloResult
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return HelloResult{}, fmt.Errorf("plugin %s: decode hello: %w", p.name, err)
	}
	if out.ID == "" || out.Label == "" {
		return HelloResult{}, fmt.Errorf("plugin %s: hello missing required id or label", p.name)
	}
	return out, nil
}
