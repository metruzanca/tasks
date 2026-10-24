package sdk

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/metruzanca/tasks/pkg/sdk/pb"
	"google.golang.org/grpc"
)

// PluginName is the key used in the go-plugin plugin map.
const PluginName = "backend"

// AuthPluginName is the key for the optional interactive login service.
const AuthPluginName = "auth"

// CredentialsFileEnv carries the path to the shared credentials file.
const CredentialsFileEnv = "TASKS_CREDENTIALS_FILE"

// Authenticator is implemented by backends that support `tasks login`.
type Authenticator interface {
	Login(ctx context.Context, input string, hasInput bool) (*pb.LoginResponse, error)
}

// Handshake is shared by the host and every backend plugin.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "TASKS_PLUGIN",
	MagicCookieValue: "tasks-v1",
}

// PluginConfigEnv carries the backend-specific section of the user's config to
// the plugin as a JSON object.
const PluginConfigEnv = "TASKS_PLUGIN_CONFIG"

// PluginNameEnv carries the configured backend name.
const PluginNameEnv = "TASKS_PLUGIN_NAME"

// Plugin implements plugin.GRPCPlugin for a Backend.
type Plugin struct {
	plugin.NetRPCUnsupportedPlugin
	Impl Backend
}

// GRPCServer registers the backend implementation on a fresh gRPC server.
func (p *Plugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterBackendServer(s, &grpcServer{impl: p.Impl})
	return nil
}

// GRPCClient returns a Backend backed by the plugin's gRPC service.
func (p *Plugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &grpcClient{client: pb.NewBackendClient(c)}, nil
}

// AuthPlugin implements plugin.GRPCPlugin for an Authenticator.
type AuthPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	Impl Authenticator
}

// GRPCServer registers the auth implementation on a fresh gRPC server.
func (p *AuthPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	pb.RegisterAuthServer(s, &grpcAuthServer{impl: p.Impl})
	return nil
}

// GRPCClient returns an Authenticator backed by the plugin's gRPC service.
func (p *AuthPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &authClient{client: pb.NewAuthClient(c)}, nil
}

// Serve runs a backend plugin. It never returns; the host terminates it.
func Serve(b Backend) {
	plugins := map[string]plugin.Plugin{
		PluginName: &Plugin{Impl: b},
	}
	if a, ok := b.(Authenticator); ok {
		plugins[AuthPluginName] = &AuthPlugin{Impl: a}
	}
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         plugins,
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}

type grpcAuthServer struct {
	pb.UnimplementedAuthServer
	impl Authenticator
}

func (s *grpcAuthServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	return s.impl.Login(ctx, req.GetInput(), req.GetHasInput())
}

type authClient struct {
	client pb.AuthClient
}

func (c *authClient) Login(ctx context.Context, input string, hasInput bool) (*pb.LoginResponse, error) {
	return c.client.Login(ctx, &pb.LoginRequest{Input: input, HasInput: hasInput})
}

type grpcServer struct {
	pb.UnimplementedBackendServer
	impl Backend
}

func (s *grpcServer) Describe(ctx context.Context, _ *pb.DescribeRequest) (*pb.BackendInfo, error) {
	return s.impl.Describe(ctx)
}

func (s *grpcServer) Setup(ctx context.Context, req *pb.SetupRequest) (*pb.SetupResponse, error) {
	msgs, err := s.impl.Setup(ctx, req.GetCreateMissing())
	if err != nil {
		return nil, err
	}
	return &pb.SetupResponse{Messages: msgs}, nil
}

func (s *grpcServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	tasks, err := s.impl.ListTasks(ctx, req.GetFilter())
	if err != nil {
		return nil, err
	}
	return &pb.ListTasksResponse{Tasks: tasks}, nil
}

func (s *grpcServer) GetTask(ctx context.Context, req *pb.GetTaskRequest) (*pb.Task, error) {
	return s.impl.GetTask(ctx, req.GetId())
}

func (s *grpcServer) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	return s.impl.CreateTask(ctx, req)
}

func (s *grpcServer) UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	return s.impl.UpdateTask(ctx, req)
}

func (s *grpcServer) MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error) {
	return s.impl.MoveTask(ctx, req)
}

func (s *grpcServer) DeleteTask(ctx context.Context, req *pb.DeleteTaskRequest) (*pb.DeleteTaskResponse, error) {
	if err := s.impl.DeleteTask(ctx, req.GetId(), req.GetHard()); err != nil {
		return nil, err
	}
	return &pb.DeleteTaskResponse{}, nil
}

func (s *grpcServer) AddComment(ctx context.Context, req *pb.AddCommentRequest) (*pb.Comment, error) {
	return s.impl.AddComment(ctx, req.GetTaskId(), req.GetBody())
}

func (s *grpcServer) ListComments(ctx context.Context, req *pb.ListCommentsRequest) (*pb.ListCommentsResponse, error) {
	comments, err := s.impl.ListComments(ctx, req.GetTaskId())
	if err != nil {
		return nil, err
	}
	return &pb.ListCommentsResponse{Comments: comments}, nil
}

func (s *grpcServer) Health(ctx context.Context, _ *pb.HealthRequest) (*pb.HealthResponse, error) {
	if err := s.impl.Health(ctx); err != nil {
		return &pb.HealthResponse{Ok: false, Message: err.Error()}, nil
	}
	return &pb.HealthResponse{Ok: true}, nil
}

type grpcClient struct {
	client pb.BackendClient
}

func (c *grpcClient) Describe(ctx context.Context) (*pb.BackendInfo, error) {
	return c.client.Describe(ctx, &pb.DescribeRequest{})
}

func (c *grpcClient) Setup(ctx context.Context, createMissing bool) ([]string, error) {
	resp, err := c.client.Setup(ctx, &pb.SetupRequest{CreateMissing: createMissing})
	if err != nil {
		return nil, err
	}
	return resp.GetMessages(), nil
}

func (c *grpcClient) ListTasks(ctx context.Context, filter *pb.TaskFilter) ([]*pb.Task, error) {
	resp, err := c.client.ListTasks(ctx, &pb.ListTasksRequest{Filter: filter})
	if err != nil {
		return nil, err
	}
	return resp.GetTasks(), nil
}

func (c *grpcClient) GetTask(ctx context.Context, id string) (*pb.Task, error) {
	return c.client.GetTask(ctx, &pb.GetTaskRequest{Id: id})
}

func (c *grpcClient) CreateTask(ctx context.Context, req *pb.CreateTaskRequest) (*pb.Task, error) {
	return c.client.CreateTask(ctx, req)
}

func (c *grpcClient) UpdateTask(ctx context.Context, req *pb.UpdateTaskRequest) (*pb.Task, error) {
	return c.client.UpdateTask(ctx, req)
}

func (c *grpcClient) MoveTask(ctx context.Context, req *pb.MoveTaskRequest) (*pb.Task, error) {
	return c.client.MoveTask(ctx, req)
}

func (c *grpcClient) DeleteTask(ctx context.Context, id string, hard bool) error {
	_, err := c.client.DeleteTask(ctx, &pb.DeleteTaskRequest{Id: id, Hard: hard})
	return err
}

func (c *grpcClient) AddComment(ctx context.Context, taskID, body string) (*pb.Comment, error) {
	return c.client.AddComment(ctx, &pb.AddCommentRequest{TaskId: taskID, Body: body})
}

func (c *grpcClient) ListComments(ctx context.Context, taskID string) ([]*pb.Comment, error) {
	resp, err := c.client.ListComments(ctx, &pb.ListCommentsRequest{TaskId: taskID})
	if err != nil {
		return nil, err
	}
	return resp.GetComments(), nil
}

func (c *grpcClient) Health(ctx context.Context) error {
	resp, err := c.client.Health(ctx, &pb.HealthRequest{})
	if err != nil {
		return err
	}
	if !resp.GetOk() {
		return fmt.Errorf("%s", resp.GetMessage())
	}
	return nil
}
