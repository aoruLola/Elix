package adapter

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestMethodConstants(t *testing.T) {
	t.Parallel()

	if MethodStartRun != "/echohelix.adapter.Adapter/StartRun" {
		t.Fatalf("unexpected MethodStartRun: %q", MethodStartRun)
	}
	if MethodStreamEvents != "/echohelix.adapter.Adapter/StreamEvents" {
		t.Fatalf("unexpected MethodStreamEvents: %q", MethodStreamEvents)
	}
	if MethodCancelRun != "/echohelix.adapter.Adapter/CancelRun" {
		t.Fatalf("unexpected MethodCancelRun: %q", MethodCancelRun)
	}
	if MethodHealth != "/echohelix.adapter.Adapter/Health" {
		t.Fatalf("unexpected MethodHealth: %q", MethodHealth)
	}
	if MethodCapabilities != "/echohelix.adapter.Adapter/Capabilities" {
		t.Fatalf("unexpected MethodCapabilities: %q", MethodCapabilities)
	}
}

func TestAdapterStreamEventsServerSendForwardsMessage(t *testing.T) {
	t.Parallel()

	stream := &fakeServerStream{}
	s := &adapterStreamEventsServer{ServerStream: stream}
	ev := &AgentEvent{RunID: "run-1"}
	if err := s.Send(ev); err != nil {
		t.Fatalf("send: %v", err)
	}
	if stream.lastSent != ev {
		t.Fatalf("expected forwarded event pointer")
	}
}

func TestAdapterStreamEventsClientRecv(t *testing.T) {
	t.Parallel()

	want := &AgentEvent{RunID: "run-1", Seq: 3}
	stream := &fakeClientStream{recvEvent: want}
	client := &adapterStreamEventsClient{ClientStream: stream}

	got, err := client.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if got.RunID != want.RunID || got.Seq != want.Seq {
		t.Fatalf("unexpected event: %#v", got)
	}
}

func TestAdapterStreamEventsClientRecvError(t *testing.T) {
	t.Parallel()

	stream := &fakeClientStream{recvErr: io.EOF}
	client := &adapterStreamEventsClient{ClientStream: stream}
	if _, err := client.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

type fakeServerStream struct {
	lastSent any
}

func (f *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeServerStream) SetTrailer(metadata.MD)       {}
func (f *fakeServerStream) Context() context.Context     { return context.Background() }
func (f *fakeServerStream) SendMsg(m any) error {
	f.lastSent = m
	return nil
}
func (f *fakeServerStream) RecvMsg(any) error { return io.EOF }

type fakeClientStream struct {
	recvEvent *AgentEvent
	recvErr   error
}

func (f *fakeClientStream) Header() (metadata.MD, error) { return nil, nil }
func (f *fakeClientStream) Trailer() metadata.MD         { return nil }
func (f *fakeClientStream) CloseSend() error             { return nil }
func (f *fakeClientStream) Context() context.Context     { return context.Background() }
func (f *fakeClientStream) SendMsg(any) error            { return nil }
func (f *fakeClientStream) RecvMsg(m any) error {
	if f.recvErr != nil {
		return f.recvErr
	}
	ev, ok := m.(*AgentEvent)
	if !ok {
		return errors.New("unexpected message type")
	}
	*ev = *f.recvEvent
	return nil
}
