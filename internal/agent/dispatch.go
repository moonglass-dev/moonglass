package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// writeTimeout bounds writing one command result to the live connection.
const writeTimeout = 10 * time.Second

// Executor runs the deploy commands; *deploy.Executor satisfies it (ADR-0014).
type Executor interface {
	Deploy(ctx context.Context, requestID string, d *agentv1.Deploy) *agentv1.CommandResult
	SwitchRelease(ctx context.Context, requestID string, s *agentv1.SwitchRelease) *agentv1.CommandResult
	Rollback(ctx context.Context, requestID string, r *agentv1.Rollback) *agentv1.CommandResult
	RunPreDeploy(ctx context.Context, requestID string, r *agentv1.RunPreDeploy) *agentv1.CommandResult
}

type command struct {
	run   func(context.Context) *agentv1.CommandResult
	id    string
	appID string
}

func newCommand[M interface {
	proto.Message
	GetAppId() string
}](env *agentv1.Envelope, msg M, run func(context.Context, string, M) *agentv1.CommandResult) (command, error) {
	if err := protocol.Open(env, msg); err != nil {
		return command{}, fmt.Errorf("connect: control plane sent a %v that does not open: %w", env.GetType(), err)
	}
	id := env.GetRequestId()
	return command{id: id, appID: msg.GetAppId(), run: func(ctx context.Context) *agentv1.CommandResult {
		return run(ctx, id, msg)
	}}, nil
}

// openCommand opens one command envelope; any other type closes the connection.
func openCommand(env *agentv1.Envelope, x Executor) (command, error) {
	switch env.GetType() {
	case agentv1.MessageType_DEPLOY:
		return newCommand(env, &agentv1.Deploy{}, x.Deploy)
	case agentv1.MessageType_SWITCH_RELEASE:
		return newCommand(env, &agentv1.SwitchRelease{}, x.SwitchRelease)
	case agentv1.MessageType_ROLLBACK:
		return newCommand(env, &agentv1.Rollback{}, x.Rollback)
	case agentv1.MessageType_RUN_PRE_DEPLOY:
		return newCommand(env, &agentv1.RunPreDeploy{}, x.RunPreDeploy)
	case agentv1.MessageType_MESSAGE_TYPE_UNSPECIFIED,
		agentv1.MessageType_ENROLL_REQUEST,
		agentv1.MessageType_ENROLL_RESPONSE,
		agentv1.MessageType_HEARTBEAT,
		agentv1.MessageType_HEARTBEAT_ACK,
		agentv1.MessageType_ERROR,
		agentv1.MessageType_COMMAND_RESULT,
		agentv1.MessageType_BUILD,
		agentv1.MessageType_STOP_APP,
		agentv1.MessageType_REMOVE_APP,
		agentv1.MessageType_UPDATE_OFFER,
		agentv1.MessageType_UPDATE_REPORT:
	}
	return command{}, fmt.Errorf("connect: control plane sent %v, which the agent does not accept: closing the connection", env.GetType())
}

type liveConn struct {
	c      *websocket.Conn
	failed chan<- error
}

// dispatcher runs commands for the whole Connect call: one FIFO per app.
type dispatcher struct {
	ctx     context.Context
	onError func(error)
	live    *liveConn
	pending map[string]bool
	queues  map[string][]command
	wg      sync.WaitGroup
	mu      sync.Mutex
}

func newDispatcher(ctx context.Context, onError func(error)) *dispatcher {
	return &dispatcher{
		ctx:     ctx,
		onError: onError,
		pending: map[string]bool{},
		queues:  map[string][]command{},
	}
}

func (d *dispatcher) attach(c *websocket.Conn, failed chan<- error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.live = &liveConn{c: c, failed: failed}
}

func (d *dispatcher) detach(c *websocket.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.live != nil && d.live.c == c {
		d.live = nil
	}
}

// submit queues cmd behind its app's running command; a pending id is ignored.
func (d *dispatcher) submit(cmd command) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending[cmd.id] {
		return
	}
	d.pending[cmd.id] = true
	d.queues[cmd.appID] = append(d.queues[cmd.appID], cmd)
	if len(d.queues[cmd.appID]) == 1 {
		d.wg.Add(1)
		go d.drain(cmd.appID)
	}
}

func (d *dispatcher) drain(appID string) {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		cmd := d.queues[appID][0]
		d.mu.Unlock()

		res := cmd.run(d.ctx)

		// The id leaves pending before its result goes out, so a re-send that
		// arrives after the result is answered by the executor's stored record.
		d.mu.Lock()
		delete(d.pending, cmd.id)
		d.mu.Unlock()
		d.send(cmd.id, res)

		d.mu.Lock()
		d.queues[appID] = d.queues[appID][1:]
		if len(d.queues[appID]) == 0 {
			delete(d.queues, appID)
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
	}
}

// send writes res as the COMMAND_RESULT of id on the live connection, or drops
// it; the stored record answers a re-send (rule 2).
func (d *dispatcher) send(id string, res *agentv1.CommandResult) {
	env, err := protocol.Wrap(agentv1.MessageType_COMMAND_RESULT, id, res)
	if err != nil {
		d.report(fmt.Errorf("connect: encode result of request %s: %w", id, err))
		return
	}
	payload, err := protocol.Marshal(env)
	if err != nil {
		d.report(fmt.Errorf("connect: encode result of request %s: %w", id, err))
		return
	}
	d.mu.Lock()
	live := d.live
	d.mu.Unlock()
	if live == nil {
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, writeTimeout)
	defer cancel()
	if err := live.c.Write(ctx, websocket.MessageBinary, payload); err != nil {
		select {
		case live.failed <- fmt.Errorf("connect: send result of request %s: %w", id, err):
		default:
		}
	}
}

func (d *dispatcher) report(err error) {
	if d.onError != nil {
		d.onError(err)
	}
}

func (d *dispatcher) wait() { d.wg.Wait() }
