package net

import (
	"context"
	"net/http"
	"os"
	"path"
	run "runtime"
	"testing"
	"time"

	"github.com/kabukky/httpscerts"
	"github.com/stretchr/testify/require"

	"github.com/drand/drand/common/log"
	testnet "github.com/drand/drand/internal/test/net"
	"github.com/drand/drand/internal/test/testlogger"
	"github.com/drand/drand/protobuf/drand"
)

type testPeer struct {
	addr string
	t    bool
}

func (t *testPeer) Address() string {
	return t.addr
}

func (t *testPeer) IsTLS() bool {
	return t.t
}

type testRandomnessServer struct {
	*testnet.EmptyServer
	round uint64
}

func (t *testRandomnessServer) PublicRand(context.Context, *drand.PublicRandRequest) (*drand.PublicRandResponse, error) {
	return &drand.PublicRandResponse{Round: t.round}, nil
}

func (t *testRandomnessServer) Group(_ context.Context, _ *drand.GroupRequest) (*drand.GroupPacket, error) {
	return nil, nil
}
func (t *testRandomnessServer) Home(context.Context, *drand.HomeRequest) (*drand.HomeResponse, error) {
	return nil, nil
}

func (t *testRandomnessServer) Packet(context.Context, *drand.GossipPacket) (*drand.EmptyDKGResponse, error) {
	return &drand.EmptyDKGResponse{}, nil
}

func (t *testRandomnessServer) Command(context.Context, *drand.DKGCommand) (*drand.EmptyDKGResponse, error) {
	return &drand.EmptyDKGResponse{}, nil
}

func TestListeners(t *testing.T) {
	t.Run("without-tls", func(t *testing.T) { testListener(t) })
	t.Run("with-tls", func(t *testing.T) { testListenerTLS(t) })
}

func testListener(t testing.TB) {
	lg := testlogger.New(t)
	ctx := log.ToContext(context.Background(), lg)
	randServer := &testRandomnessServer{round: 42}

	lisGRPC, err := NewGRPCListenerForPrivate(ctx, "127.0.0.1:", "", "", randServer, true)
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(resp http.ResponseWriter, r *http.Request) { resp.Write([]byte("ok")) })
	lisREST, err := NewRESTListenerForPublic(ctx, "127.0.0.1:", "", "", mux, true)
	require.NoError(t, err)

	peerGRPC := &testPeer{lisGRPC.Addr(), false}

	go lisGRPC.Start()
	defer lisGRPC.Stop(ctx)
	go lisREST.Start()
	defer lisREST.Stop(ctx)
	time.Sleep(100 * time.Millisecond)

	// GRPC
	client := NewGrpcClient(lg)
	resp, err := client.PublicRand(ctx, peerGRPC, &drand.PublicRandRequest{})
	require.NoError(t, err)
	expected := &drand.PublicRandResponse{Round: randServer.round}
	require.Equal(t, expected.GetRound(), resp.GetRound())
}

// ref https://bbengfort.github.io/programmer/2017/03/03/secure-grpc.html
func testListenerTLS(t testing.TB) {
	lg := testlogger.New(t)
	ctx := log.ToContext(context.Background(), lg)

	if run.GOOS == runtimeGOOSWindows {
		t.Log("Skipping TestClientTLS as operating on Windows")
		t.Skip("crypto/x509: system root pool is not available on Windows")
	}
	hostAddr := "127.0.0.1"

	tmpDir := path.Join(t.TempDir(), "drand-net")
	require.NoError(t, os.MkdirAll(tmpDir, 0766))

	certPath := path.Join(tmpDir, "server.crt")
	keyPath := path.Join(tmpDir, "server.key")
	if httpscerts.Check(certPath, keyPath) != nil {
		require.NoError(t, httpscerts.Generate(certPath, keyPath, hostAddr))
	}

	randServer := &testRandomnessServer{round: 42}

	lisGRPC, err := NewGRPCListenerForPrivate(ctx, hostAddr+":", certPath, keyPath, randServer, false)
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(resp http.ResponseWriter, r *http.Request) { resp.Write([]byte("ok")) })
	lisREST, err := NewRESTListenerForPublic(ctx, hostAddr+":", certPath, keyPath, mux, false)
	require.NoError(t, err)

	peerGRPC := &testPeer{lisGRPC.Addr(), true}

	go lisGRPC.Start()
	defer lisGRPC.Stop(ctx)
	go lisREST.Start()
	defer lisREST.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	certManager := NewCertManager(lg)
	err = certManager.Add(certPath)
	require.NoError(t, err)

	// test GRPC variant
	client := NewGrpcClientFromCertManager(lg, certManager)
	resp, err := client.PublicRand(ctx, peerGRPC, &drand.PublicRandRequest{})
	require.Nil(t, err)
	expected := &drand.PublicRandResponse{Round: randServer.round}
	require.Equal(t, expected.GetRound(), resp.GetRound())
}