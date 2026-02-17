package codex

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"echohelix/internal/adapter/supervisor"
	"echohelix/internal/driver"
	"echohelix/internal/events"
	adapterrpc "echohelix/internal/rpc/adapter"
	"echohelix/internal/rpc/codec"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Driver struct {
	addr       string
	supervisor *supervisor.Supervisor

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client adapterrpc.AdapterClient
}

func New(addr string, sup *supervisor.Supervisor) *Driver {
	return &Driver{
		addr:       addr,
		supervisor: sup,
	}
}

func (d *Driver) Name() string {
	return "codex"
}

func (d *Driver) StartRun(ctx context.Context, req driver.StartRequest) (*driver.Stream, error) {
	client, err := d.getClient(ctx)
	if err != nil {
		return nil, err
	}

	timeoutSec := int32(1800)
	if deadline, ok := ctx.Deadline(); ok {
		timeoutSec = int32(time.Until(deadline).Seconds())
		if timeoutSec <= 0 {
			timeoutSec = 1
		}
	}

	res, err := client.StartRun(ctx, &adapterrpc.StartRunRequest{
		RunID:         req.RunID,
		WorkspacePath: req.WorkspacePath,
		Prompt:        req.Prompt,
		Context:       req.Context,
		Model:         req.Options.Model,
		Profile:       req.Options.Profile,
		Sandbox:       req.Options.Sandbox,
		SchemaVersion: req.Options.SchemaVersion,
		TimeoutSec:    timeoutSec,
	})
	if err != nil {
		return nil, err
	}
	if !res.Accepted {
		return nil, fmt.Errorf("adapter rejected run: %s", res.Error)
	}

	eventsCh := make(chan events.Event, 128)
	doneCh := make(chan error, 1)
	go d.consumeEvents(ctx, req, client, eventsCh, doneCh)
	return &driver.Stream{Events: eventsCh, Done: doneCh}, nil
}

func (d *Driver) consumeEvents(
	ctx context.Context,
	req driver.StartRequest,
	client adapterrpc.AdapterClient,
	eventsCh chan<- events.Event,
	doneCh chan<- error,
) {
	defer close(eventsCh)
	defer close(doneCh)

	stream, err := client.StreamEvents(ctx, &adapterrpc.StreamEventsRequest{RunID: req.RunID})
	if err != nil {
		doneCh <- err
		return
	}
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			doneCh <- nil
			return
		}
		if err != nil {
			doneCh <- err
			return
		}

		e := events.Event{
			RunID:         ev.RunID,
			TS:            time.Unix(ev.TsUnix, 0).UTC(),
			SchemaVersion: ev.SchemaVersion,
			Type:          ev.Type,
			Channel:       ev.Channel,
			Format:        ev.Format,
			Role:          ev.Role,
			Compat: &events.CompatFields{
				Text:    ev.CompatText,
				Status:  ev.CompatStatus,
				IsError: ev.CompatIsError,
			},
			Payload: ev.Payload,
			Backend: d.Name(),
			Source:  ev.Source,
		}
		select {
		case eventsCh <- e:
		case <-ctx.Done():
			doneCh <- ctx.Err()
			return
		}
	}
}

func (d *Driver) Cancel(ctx context.Context, runID string) error {
	client, err := d.getClient(ctx)
	if err != nil {
		return err
	}
	res, err := client.CancelRun(ctx, &adapterrpc.CancelRunRequest{RunID: runID})
	if err != nil {
		return err
	}
	if !res.Cancelled {
		return fmt.Errorf("adapter refused cancel: %s", res.Error)
	}
	return nil
}

func (d *Driver) Health(ctx context.Context) (driver.Health, error) {
	client, err := d.getClient(ctx)
	if err != nil {
		return driver.Health{OK: false, Message: err.Error()}, err
	}
	res, err := client.Health(ctx, &adapterrpc.HealthRequest{})
	if err != nil {
		return driver.Health{OK: false, Message: err.Error()}, err
	}
	return driver.Health{OK: res.OK, Message: res.Message}, nil
}

func (d *Driver) Capabilities(ctx context.Context) (driver.CapabilitySet, error) {
	client, err := d.getClient(ctx)
	if err != nil {
		return driver.CapabilitySet{}, err
	}
	res, err := client.Capabilities(ctx, &adapterrpc.CapabilitiesRequest{})
	if err != nil {
		return driver.CapabilitySet{}, err
	}
	return driver.CapabilitySet{
		Backend:                res.Backend,
		EventTypes:             res.EventTypes,
		SupportsCancel:         res.SupportsCancel,
		SupportsPTY:            res.SupportsPTY,
		SchemaVersions:         res.SchemaVersions,
		PreferredSchemaVersion: res.PreferredSchemaVersion,
		CompatFields:           res.CompatFields,
	}, nil
}

func (d *Driver) getClient(ctx context.Context) (adapterrpc.AdapterClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.supervisor.EnsureRunning(ctx); err != nil {
		return nil, err
	}
	if d.client != nil {
		return d.client, nil
	}

	conn, err := grpc.DialContext(
		ctx,
		d.addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.JSONCodec{})),
	)
	if err != nil {
		return nil, err
	}
	d.conn = conn
	d.client = adapterrpc.NewAdapterClient(conn)
	return d.client, nil
}
