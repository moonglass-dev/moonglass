package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// commandResult is what the fake executor returns: a result that is valid on
// the wire, so the dispatcher's encode succeeds.
func commandResult() *agentv1.CommandResult {
	return &agentv1.CommandResult{
		Outcome: agentv1.CommandOutcome_SUCCEEDED,
		App:     &agentv1.AppState{Phase: agentv1.ReleasePhase_NONE},
	}
}

// execCall is one command the fake executor ran.
type execCall struct {
	ID  string
	App string
}

// fakeExec is the test Executor. It records every call in arrival order, counts
// the calls per request id, tracks the concurrent calls per app, and holds a
// call on its gate channel when the test registered one.
type fakeExec struct {
	mu      sync.Mutex
	calls   []execCall
	counts  map[string]int
	running map[string]int
	maxRun  map[string]int
	gates   map[string]chan struct{}
	hold    time.Duration
}

func newFakeExec() *fakeExec {
	return &fakeExec{
		counts:  map[string]int{},
		running: map[string]int{},
		maxRun:  map[string]int{},
		gates:   map[string]chan struct{}{},
	}
}

// gate registers a release channel for request id and returns it. The executor
// blocks on it until the test closes it.
func (f *fakeExec) gate(id string) chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{})
	f.gates[id] = ch
	return ch
}

func (f *fakeExec) run(ctx context.Context, id, app string) *agentv1.CommandResult {
	f.mu.Lock()
	f.calls = append(f.calls, execCall{ID: id, App: app})
	f.counts[id]++
	f.running[app]++
	if f.running[app] > f.maxRun[app] {
		f.maxRun[app] = f.running[app]
	}
	gate := f.gates[id]
	hold := f.hold
	f.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	if hold > 0 {
		time.Sleep(hold)
	}

	f.mu.Lock()
	f.running[app]--
	f.mu.Unlock()
	return commandResult()
}

func (f *fakeExec) Deploy(ctx context.Context, id string, d *agentv1.Deploy) *agentv1.CommandResult {
	return f.run(ctx, id, d.GetAppId())
}

func (f *fakeExec) SwitchRelease(ctx context.Context, id string, s *agentv1.SwitchRelease) *agentv1.CommandResult {
	return f.run(ctx, id, s.GetAppId())
}

func (f *fakeExec) Rollback(ctx context.Context, id string, r *agentv1.Rollback) *agentv1.CommandResult {
	return f.run(ctx, id, r.GetAppId())
}

func (f *fakeExec) RunPreDeploy(ctx context.Context, id string, r *agentv1.RunPreDeploy) *agentv1.CommandResult {
	return f.run(ctx, id, r.GetAppId())
}

func (f *fakeExec) callsSeen() []execCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execCall(nil), f.calls...)
}

func (f *fakeExec) callCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[id]
}

func (f *fakeExec) maxConcurrent(app string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxRun[app]
}

func (f *fakeExec) runningCount(app string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[app]
}

// cpResult is one COMMAND_RESULT the fake control plane received.
type cpResult struct {
	Res *agentv1.CommandResult
	ID  string
	// Conn is the number of the connection the result arrived on.
	Conn int
}

// cmdCP is a fake control plane that acks every Heartbeat, records every
// COMMAND_RESULT with its connection number, and lets the test send frames on
// the live connection and drop it.
type cmdCP struct {
	t       *testing.T
	cp      *fakeCP
	mu      sync.Mutex
	writeMu sync.Mutex
	live    *websocket.Conn
	conns   int
	beats   int
	acks    map[int]bool
	results chan cpResult
}

// newCmdCP starts a fake control plane and returns it with an enrolled agent
// data dir pinned to it.
func newCmdCP(t *testing.T) (*cmdCP, string) {
	t.Helper()
	pin, priv := genKey(t)
	dir, _ := writeAgent(t, pin)
	cp := &cmdCP{t: t, acks: map[int]bool{}, results: make(chan cpResult, 64)}
	cp.cp = newFakeCP(t, cpOpts{priv: priv, handle: cp.handle})
	return cp, dir
}

func (cp *cmdCP) handle(ctx context.Context, _ *fakeCP, c *websocket.Conn) {
	cp.mu.Lock()
	cp.conns++
	n := cp.conns
	cp.live = c
	cp.mu.Unlock()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			return
		}
		env, err := protocol.Unmarshal(data)
		if err != nil {
			return
		}
		switch env.GetType() {
		case agentv1.MessageType_HEARTBEAT:
			cp.mu.Lock()
			cp.beats++
			cp.mu.Unlock()
			if err := cp.writeOn(ctx, c, mustMarshal(cp.t, agentv1.MessageType_HEARTBEAT_ACK, env.GetRequestId(), &agentv1.HeartbeatAck{})); err != nil {
				return
			}
			cp.mu.Lock()
			cp.acks[n] = true
			cp.mu.Unlock()
		case agentv1.MessageType_COMMAND_RESULT:
			var res agentv1.CommandResult
			if err := protocol.Open(env, &res); err != nil {
				return
			}
			cp.results <- cpResult{Res: &res, ID: env.GetRequestId(), Conn: n}
		default:
			return
		}
	}
}

func (cp *cmdCP) writeOn(ctx context.Context, c *websocket.Conn, data []byte) error {
	cp.writeMu.Lock()
	defer cp.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageBinary, data)
}

// send writes one framed envelope on the connection that is live now.
func (cp *cmdCP) send(data []byte) error {
	cp.mu.Lock()
	c := cp.live
	cp.mu.Unlock()
	if c == nil {
		return errNoLiveConnection
	}
	return cp.writeOn(context.Background(), c, data)
}

var errNoLiveConnection = errors.New("dispatch_test: no live control-plane connection")

// closeLive drops the connection that is live now, so the agent reconnects.
func (cp *cmdCP) closeLive() {
	cp.mu.Lock()
	c := cp.live
	cp.mu.Unlock()
	if c != nil {
		_ = c.Close(websocket.StatusNormalClosure, "drop")
	}
}

func (cp *cmdCP) connCount() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.conns
}

func (cp *cmdCP) beatCount() int {
	cp.mu.Lock()
	defer cp.mu.Unlock()
	return cp.beats
}

// waitConn waits for the numbered connection to be accepted and for its first
// Heartbeat to be acked, so a command frame sent now is read by that session.
func (cp *cmdCP) waitConn(t *testing.T, n int) {
	t.Helper()
	waitFor(t, "connection number "+strconv.Itoa(n), func() bool { return cp.connCount() >= n })
	waitFor(t, "the first heartbeat ack on connection "+strconv.Itoa(n), func() bool {
		cp.mu.Lock()
		defer cp.mu.Unlock()
		for k := 1; k <= n; k++ {
			if !cp.acks[k] {
				return false
			}
		}
		return true
	})
}

func (cp *cmdCP) nextResult(t *testing.T) cpResult {
	t.Helper()
	select {
	case r := <-cp.results:
		return r
	case <-time.After(10 * time.Second):
		t.Fatalf("no COMMAND_RESULT within 10s, want one")
		return cpResult{}
	}
}

// expectNoResult fails when another COMMAND_RESULT arrives within d.
func (cp *cmdCP) expectNoResult(t *testing.T, d time.Duration, what string) {
	t.Helper()
	select {
	case r := <-cp.results:
		t.Fatalf("COMMAND_RESULT %q on connection %d: expected none (%s)", r.ID, r.Conn, what)
	case <-time.After(d):
	}
}

// rawEnvelope frames payload under typ without opening or validating it, so a
// test can send a payload that protocol.Open must reject.
func rawEnvelope(t *testing.T, typ agentv1.MessageType, id string, payload []byte) []byte {
	t.Helper()
	b, err := protocol.Marshal(&agentv1.Envelope{Version: protocol.Version, Type: typ, RequestId: id, Payload: payload})
	if err != nil {
		t.Fatalf("protocol.Marshal envelope type %v: unexpected error %v", typ, err)
	}
	return b
}

// deployMessage is one Deploy the protocol boundary accepts.
func deployMessage(app, rel string) *agentv1.Deploy {
	return &agentv1.Deploy{
		AppId:       app,
		ReleaseId:   rel,
		Image:       "registry.example.com/acme/web:" + rel,
		Project:     "proj",
		Environment: "production",
		Service:     "web",
	}
}

func TestCommandsForOneAppRunInArrivalOrder(t *testing.T) {
	cp, dir := newCmdCP(t)
	exec := newFakeExec()
	exec.hold = 20 * time.Millisecond

	_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
		Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
	})
	cp.waitConn(t, 1)

	frames := [][]byte{
		mustMarshal(t, agentv1.MessageType_DEPLOY, "c1", deployMessage("app-a", "r1")),
		mustMarshal(t, agentv1.MessageType_SWITCH_RELEASE, "c2", &agentv1.SwitchRelease{AppId: "app-a", ReleaseId: "r1"}),
		mustMarshal(t, agentv1.MessageType_ROLLBACK, "c3", &agentv1.Rollback{AppId: "app-a"}),
	}
	for _, frame := range frames {
		if err := cp.send(frame); err != nil {
			t.Fatalf("send a command: unexpected error %v", err)
		}
	}

	wantIDs := []string{"c1", "c2", "c3"}
	for i, want := range wantIDs {
		r := cp.nextResult(t)
		if r.ID != want {
			t.Errorf("result %d: request id = %q, want %q", i, r.ID, want)
		}
	}

	calls := exec.callsSeen()
	gotIDs := make([]string, 0, len(calls))
	for _, c := range calls {
		gotIDs = append(gotIDs, c.ID)
	}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("executor calls = %v, want the arrival order %v", gotIDs, wantIDs)
	}
	if got := exec.maxConcurrent("app-a"); got != 1 {
		t.Errorf("maximum concurrent calls for app-a = %d, want 1", got)
	}
	_ = stop()
}

func TestCommandsForDifferentAppsRunConcurrently(t *testing.T) {
	cp, dir := newCmdCP(t)
	exec := newFakeExec()
	gateA := exec.gate("c1")

	_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
		Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
	})
	cp.waitConn(t, 1)

	if err := cp.send(mustMarshal(t, agentv1.MessageType_DEPLOY, "c1", deployMessage("app-a", "r1"))); err != nil {
		t.Fatalf("send the command for app-a: unexpected error %v", err)
	}
	waitFor(t, "the command for app-a to start", func() bool { return exec.callCount("c1") == 1 })

	if err := cp.send(mustMarshal(t, agentv1.MessageType_DEPLOY, "c2", deployMessage("app-b", "r1"))); err != nil {
		t.Fatalf("send the command for app-b: unexpected error %v", err)
	}

	r := cp.nextResult(t)
	if r.ID != "c2" {
		t.Fatalf("first finished command = %q with outcome %v, want %q while app-a is still blocked", r.ID, r.Res.GetOutcome(), "c2")
	}
	if got := exec.runningCount("app-a"); got != 1 {
		t.Errorf("app-a commands still running = %d, want 1 when app-b's result arrived", got)
	}

	close(gateA)
	r = cp.nextResult(t)
	if r.ID != "c1" {
		t.Errorf("next finished command = %q, want %q", r.ID, "c1")
	}
	_ = stop()
}

func TestRunningRequestIDIsNotStartedTwice(t *testing.T) {
	t.Run("running command re-sent after a drop", func(t *testing.T) {
		cp, dir := newCmdCP(t)
		exec := newFakeExec()
		gate := exec.gate("c1")

		_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
			Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
		})
		cp.waitConn(t, 1)

		frame := mustMarshal(t, agentv1.MessageType_DEPLOY, "c1", deployMessage("app-a", "r1"))
		if err := cp.send(frame); err != nil {
			t.Fatalf("send c1 on connection 1: unexpected error %v", err)
		}
		waitFor(t, "c1 to start", func() bool { return exec.callCount("c1") == 1 })

		cp.closeLive()
		cp.waitConn(t, 2)

		if err := cp.send(frame); err != nil {
			t.Fatalf("re-send c1 on connection 2: unexpected error %v", err)
		}
		if err := cp.send(mustMarshal(t, agentv1.MessageType_DEPLOY, "c3", deployMessage("app-b", "r1"))); err != nil {
			t.Fatalf("send the sentinel command for app-b: unexpected error %v", err)
		}
		if r := cp.nextResult(t); r.ID != "c3" {
			t.Fatalf("sentinel result = %q, want %q: the re-sent c1 was not read first", r.ID, "c3")
		}
		if got := exec.callCount("c1"); got != 1 {
			t.Errorf("c1 executor calls before it finished = %d, want 1", got)
		}

		close(gate)
		r := cp.nextResult(t)
		if r.ID != "c1" {
			t.Errorf("result after release = %q, want %q", r.ID, "c1")
		}
		if r.Conn != 2 {
			t.Errorf("c1's result arrived on connection %d, want 2 (the live one when it finished)", r.Conn)
		}
		if got := exec.callCount("c1"); got != 1 {
			t.Errorf("c1 executor calls = %d, want 1", got)
		}
		cp.expectNoResult(t, 300*time.Millisecond, "c1 must be answered once")
		_ = stop()
	})

	t.Run("queued command re-sent", func(t *testing.T) {
		cp, dir := newCmdCP(t)
		exec := newFakeExec()
		gate := exec.gate("c1")

		_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
			Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
		})
		cp.waitConn(t, 1)

		c1 := mustMarshal(t, agentv1.MessageType_DEPLOY, "c1", deployMessage("app-a", "r1"))
		c2 := mustMarshal(t, agentv1.MessageType_ROLLBACK, "c2", &agentv1.Rollback{AppId: "app-a"})
		for _, frame := range [][]byte{c1, c2, c2} {
			if err := cp.send(frame); err != nil {
				t.Fatalf("send a frame for app-a: unexpected error %v", err)
			}
		}
		waitFor(t, "c1 to start", func() bool { return exec.callCount("c1") == 1 })

		if err := cp.send(mustMarshal(t, agentv1.MessageType_DEPLOY, "c3", deployMessage("app-b", "r1"))); err != nil {
			t.Fatalf("send the sentinel command for app-b: unexpected error %v", err)
		}
		if r := cp.nextResult(t); r.ID != "c3" {
			t.Fatalf("sentinel result = %q, want %q: the re-sent c2 was not read first", r.ID, "c3")
		}

		close(gate)
		ids := []string{cp.nextResult(t).ID, cp.nextResult(t).ID}
		if got := strings.Join(ids, ","); got != "c1,c2" {
			t.Fatalf("results after release = %v, want c1 then c2", ids)
		}
		if got := exec.callCount("c2"); got != 1 {
			t.Errorf("c2 executor calls = %d, want 1", got)
		}
		if got := exec.callCount("c1"); got != 1 {
			t.Errorf("c1 executor calls = %d, want 1", got)
		}
		cp.expectNoResult(t, 300*time.Millisecond, "c2 must be answered once")
		_ = stop()
	})
}

// TestResendCrossingItsResultIsNotAnsweredTwice covers a re-send that the
// control plane wrote before it read the result: the agent has already written
// the result on that connection when the re-send arrives, so it must neither
// run the command again nor send a second COMMAND_RESULT on that connection.
func TestResendCrossingItsResultIsNotAnsweredTwice(t *testing.T) {
	cp, dir := newCmdCP(t)
	exec := newFakeExec()

	_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
		Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
	})
	cp.waitConn(t, 1)

	frame := mustMarshal(t, agentv1.MessageType_DEPLOY, "c1", deployMessage("app-a", "r1"))
	if err := cp.send(frame); err != nil {
		t.Fatalf("send c1: unexpected error %v", err)
	}
	if r := cp.nextResult(t); r.ID != "c1" || r.Conn != 1 {
		t.Fatalf("first result = %q on connection %d, want %q on connection 1", r.ID, r.Conn, "c1")
	}

	if err := cp.send(frame); err != nil {
		t.Fatalf("re-send c1 on the same connection: unexpected error %v", err)
	}
	if err := cp.send(mustMarshal(t, agentv1.MessageType_DEPLOY, "c3", deployMessage("app-b", "r1"))); err != nil {
		t.Fatalf("send the sentinel command for app-b: unexpected error %v", err)
	}
	if r := cp.nextResult(t); r.ID != "c3" {
		t.Fatalf("result after the re-send = %q, want only the sentinel %q: c1 was answered twice on connection 1", r.ID, "c3")
	}
	cp.expectNoResult(t, 300*time.Millisecond, "c1 was already answered on this connection")
	if got := exec.callCount("c1"); got != 1 {
		t.Errorf("c1 executor calls = %d, want 1", got)
	}
	_ = stop()
}

func TestAgentClosesOnMessageItDoesNotAccept(t *testing.T) {
	badDeploy, err := proto.Marshal(&agentv1.Deploy{AppId: "Bad Id"})
	if err != nil {
		t.Fatalf("proto.Marshal a Deploy the agent must reject: unexpected error %v", err)
	}

	tests := []struct {
		name  string
		frame func(t *testing.T) []byte
	}{
		{"BUILD", func(t *testing.T) []byte {
			return mustMarshal(t, agentv1.MessageType_BUILD, "c1", &agentv1.Build{
				AppId: "app-a", ReleaseId: "r1", Project: "proj", Environment: "production", Service: "web",
				Repository: "acme/web", Commit: strings.Repeat("a", 40),
			})
		}},
		{"STOP_APP", func(t *testing.T) []byte {
			return mustMarshal(t, agentv1.MessageType_STOP_APP, "c1", &agentv1.StopApp{AppId: "app-a"})
		}},
		{"REMOVE_APP", func(t *testing.T) []byte {
			return mustMarshal(t, agentv1.MessageType_REMOVE_APP, "c1", &agentv1.RemoveApp{AppId: "app-a"})
		}},
		{"UPDATE_OFFER", func(t *testing.T) []byte {
			return mustMarshal(t, agentv1.MessageType_UPDATE_OFFER, "c1", &agentv1.UpdateOffer{Version: "v1.2.3"})
		}},
		{"COMMAND_RESULT", func(t *testing.T) []byte {
			return mustMarshal(t, agentv1.MessageType_COMMAND_RESULT, "c1", commandResult())
		}},
		{"HEARTBEAT", func(t *testing.T) []byte {
			return mustMarshal(t, agentv1.MessageType_HEARTBEAT, "c1", &agentv1.Heartbeat{AgentVersion: testVersion})
		}},
		{"DEPLOY whose payload does not open", func(t *testing.T) []byte {
			return rawEnvelope(t, agentv1.MessageType_DEPLOY, "c1", badDeploy)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp, dir := newCmdCP(t)
			exec := newFakeExec()

			_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
				Executor: exec, AgentVersion: testVersion, HeartbeatInterval: time.Hour, Sleep: fastSleep,
			})
			cp.waitConn(t, 1)

			if err := cp.send(tt.frame(t)); err != nil {
				t.Fatalf("send a %s frame: unexpected error %v", tt.name, err)
			}
			cp.waitConn(t, 2)
			if n := len(exec.callsSeen()); n != 0 {
				t.Errorf("executor calls after a %s: got %d, want 0", tt.name, n)
			}
			_ = stop()
		})
	}
}

func TestHeartbeatsContinueWhileCommandRuns(t *testing.T) {
	cp, dir := newCmdCP(t)
	exec := newFakeExec()
	gate := exec.gate("c1")

	_, stop := runConnect(t, dir, "https://"+cp.cp.addr, ConnectOptions{
		Executor: exec, AgentVersion: testVersion, HeartbeatInterval: 20 * time.Millisecond, Sleep: fastSleep,
	})
	cp.waitConn(t, 1)

	if err := cp.send(mustMarshal(t, agentv1.MessageType_DEPLOY, "c1", deployMessage("app-a", "r1"))); err != nil {
		t.Fatalf("send the command: unexpected error %v", err)
	}
	waitFor(t, "the command to start", func() bool { return exec.callCount("c1") == 1 })

	before := cp.beatCount()
	waitFor(t, "five heartbeats while the command runs", func() bool { return cp.beatCount() >= before+5 })
	if got := cp.connCount(); got != 1 {
		t.Errorf("connections while the command runs and its acks arrive = %d, want 1", got)
	}

	close(gate)
	if r := cp.nextResult(t); r.ID != "c1" {
		t.Errorf("result = %q, want %q", r.ID, "c1")
	}
	_ = stop()
}

func TestConnectRefusesNilExecutor(t *testing.T) {
	cp, _ := newCmdCP(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Connect(ctx, t.TempDir(), "https://"+cp.cp.addr, ConnectOptions{AgentVersion: testVersion, Sleep: fastSleep})
	if err == nil {
		t.Fatalf("Connect with no Executor: error = nil, want one naming ConnectOptions.Executor")
	}
	if !strings.Contains(err.Error(), "ConnectOptions.Executor") {
		t.Errorf("Connect with no Executor: error = %q, want it to name ConnectOptions.Executor", err)
	}
	if n := cp.cp.count(&cp.cp.accepted); n != 0 {
		t.Errorf("TCP connections to the control plane = %d, want 0: the refusal is before any dial", n)
	}
}
