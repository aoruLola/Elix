package adapter

import (
	"context"

	"google.golang.org/grpc"
)

const (
	ServiceName = "echohelix.adapter.Adapter"

	MethodStartRun     = "/" + ServiceName + "/StartRun"
	MethodStreamEvents = "/" + ServiceName + "/StreamEvents"
	MethodCancelRun    = "/" + ServiceName + "/CancelRun"
	MethodHealth       = "/" + ServiceName + "/Health"
	MethodCapabilities = "/" + ServiceName + "/Capabilities"
)

type StartRunRequest struct {
	RunID         string         `json:"run_id"`
	WorkspacePath string         `json:"workspace_path"`
	Prompt        string         `json:"prompt"`
	Context       map[string]any `json:"context,omitempty"`
	Model         string         `json:"model,omitempty"`
	Profile       string         `json:"profile,omitempty"`
	Sandbox       string         `json:"sandbox,omitempty"`
	SchemaVersion string         `json:"schema_version,omitempty"`
	TimeoutSec    int32          `json:"timeout_sec"`
}

type StartRunResponse struct {
	Accepted bool   `json:"accepted"`
	Error    string `json:"error,omitempty"`
}

type StreamEventsRequest struct {
	RunID string `json:"run_id"`
}

type CancelRunRequest struct {
	RunID string `json:"run_id"`
}

type CancelRunResponse struct {
	Cancelled bool   `json:"cancelled"`
	Error     string `json:"error,omitempty"`
}

type HealthRequest struct{}

type HealthResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type CapabilitiesRequest struct{}

type CapabilitiesResponse struct {
	Backend                string   `json:"backend"`
	EventTypes             []string `json:"event_types"`
	SupportsCancel         bool     `json:"supports_cancel"`
	SupportsPTY            bool     `json:"supports_pty"`
	SchemaVersions         []string `json:"schema_versions,omitempty"`
	PreferredSchemaVersion string   `json:"preferred_schema_version,omitempty"`
	CompatFields           []string `json:"compat_fields,omitempty"`
}

type AgentEvent struct {
	RunID         string         `json:"run_id"`
	Seq           int64          `json:"seq"`
	TsUnix        int64          `json:"ts_unix"`
	SchemaVersion string         `json:"schema_version,omitempty"`
	Type          string         `json:"type"`
	Channel       string         `json:"channel,omitempty"`
	Format        string         `json:"format,omitempty"`
	Role          string         `json:"role,omitempty"`
	CompatText    string         `json:"compat_text,omitempty"`
	CompatStatus  string         `json:"compat_status,omitempty"`
	CompatIsError bool           `json:"compat_is_error,omitempty"`
	Payload       map[string]any `json:"payload,omitempty"`
	Source        string         `json:"source,omitempty"`
}

type AdapterServer interface {
	StartRun(context.Context, *StartRunRequest) (*StartRunResponse, error)
	StreamEvents(*StreamEventsRequest, AdapterStreamEventsServer) error
	CancelRun(context.Context, *CancelRunRequest) (*CancelRunResponse, error)
	Health(context.Context, *HealthRequest) (*HealthResponse, error)
	Capabilities(context.Context, *CapabilitiesRequest) (*CapabilitiesResponse, error)
}

type AdapterStreamEventsServer interface {
	Send(*AgentEvent) error
	grpc.ServerStream
}

type adapterStreamEventsServer struct {
	grpc.ServerStream
}

func (s *adapterStreamEventsServer) Send(ev *AgentEvent) error {
	return s.ServerStream.SendMsg(ev)
}

func RegisterAdapterServer(registrar grpc.ServiceRegistrar, srv AdapterServer) {
	registrar.RegisterService(&AdapterServiceDesc, srv)
}

var AdapterServiceDesc = grpc.ServiceDesc{
	ServiceName: ServiceName,
	HandlerType: (*AdapterServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "StartRun", Handler: _Adapter_StartRun_Handler},
		{MethodName: "CancelRun", Handler: _Adapter_CancelRun_Handler},
		{MethodName: "Health", Handler: _Adapter_Health_Handler},
		{MethodName: "Capabilities", Handler: _Adapter_Capabilities_Handler},
	},
	Streams: []grpc.StreamDesc{
		{StreamName: "StreamEvents", Handler: _Adapter_StreamEvents_Handler, ServerStreams: true},
	},
	Metadata: "proto/adapter.proto",
}

func _Adapter_StartRun_Handler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(StartRunRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AdapterServer).StartRun(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MethodStartRun,
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(AdapterServer).StartRun(ctx, req.(*StartRunRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Adapter_CancelRun_Handler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(CancelRunRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AdapterServer).CancelRun(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MethodCancelRun,
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(AdapterServer).CancelRun(ctx, req.(*CancelRunRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Adapter_Health_Handler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(HealthRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AdapterServer).Health(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MethodHealth,
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(AdapterServer).Health(ctx, req.(*HealthRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Adapter_Capabilities_Handler(
	srv any,
	ctx context.Context,
	dec func(any) error,
	interceptor grpc.UnaryServerInterceptor,
) (any, error) {
	in := new(CapabilitiesRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AdapterServer).Capabilities(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: MethodCapabilities,
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(AdapterServer).Capabilities(ctx, req.(*CapabilitiesRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Adapter_StreamEvents_Handler(srv any, stream grpc.ServerStream) error {
	in := new(StreamEventsRequest)
	if err := stream.RecvMsg(in); err != nil {
		return err
	}
	return srv.(AdapterServer).StreamEvents(in, &adapterStreamEventsServer{ServerStream: stream})
}

type AdapterClient interface {
	StartRun(ctx context.Context, in *StartRunRequest, opts ...grpc.CallOption) (*StartRunResponse, error)
	StreamEvents(ctx context.Context, in *StreamEventsRequest, opts ...grpc.CallOption) (AdapterStreamEventsClient, error)
	CancelRun(ctx context.Context, in *CancelRunRequest, opts ...grpc.CallOption) (*CancelRunResponse, error)
	Health(ctx context.Context, in *HealthRequest, opts ...grpc.CallOption) (*HealthResponse, error)
	Capabilities(ctx context.Context, in *CapabilitiesRequest, opts ...grpc.CallOption) (*CapabilitiesResponse, error)
}

type adapterClient struct {
	cc grpc.ClientConnInterface
}

func NewAdapterClient(cc grpc.ClientConnInterface) AdapterClient {
	return &adapterClient{cc: cc}
}

func (c *adapterClient) StartRun(ctx context.Context, in *StartRunRequest, opts ...grpc.CallOption) (*StartRunResponse, error) {
	out := new(StartRunResponse)
	err := c.cc.Invoke(ctx, MethodStartRun, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *adapterClient) CancelRun(ctx context.Context, in *CancelRunRequest, opts ...grpc.CallOption) (*CancelRunResponse, error) {
	out := new(CancelRunResponse)
	err := c.cc.Invoke(ctx, MethodCancelRun, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *adapterClient) Health(ctx context.Context, in *HealthRequest, opts ...grpc.CallOption) (*HealthResponse, error) {
	out := new(HealthResponse)
	err := c.cc.Invoke(ctx, MethodHealth, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *adapterClient) Capabilities(ctx context.Context, in *CapabilitiesRequest, opts ...grpc.CallOption) (*CapabilitiesResponse, error) {
	out := new(CapabilitiesResponse)
	err := c.cc.Invoke(ctx, MethodCapabilities, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type AdapterStreamEventsClient interface {
	Recv() (*AgentEvent, error)
	grpc.ClientStream
}

type adapterStreamEventsClient struct {
	grpc.ClientStream
}

func (x *adapterStreamEventsClient) Recv() (*AgentEvent, error) {
	ev := new(AgentEvent)
	if err := x.ClientStream.RecvMsg(ev); err != nil {
		return nil, err
	}
	return ev, nil
}

func (c *adapterClient) StreamEvents(ctx context.Context, in *StreamEventsRequest, opts ...grpc.CallOption) (AdapterStreamEventsClient, error) {
	stream, err := c.cc.NewStream(ctx, &AdapterServiceDesc.Streams[0], MethodStreamEvents, opts...)
	if err != nil {
		return nil, err
	}
	client := &adapterStreamEventsClient{ClientStream: stream}
	if err := client.SendMsg(in); err != nil {
		return nil, err
	}
	if err := client.CloseSend(); err != nil {
		return nil, err
	}
	return client, nil
}
