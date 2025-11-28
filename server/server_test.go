package server

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ndsky1003/net/conn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pingServer repeatedly attempts to connect to the given address until successful or a timeout.
func pingServer(addr string, timeout time.Duration) error {
	done := time.After(timeout)
	for {
		select {
		case <-done:
			return fmt.Errorf("pingServer timed out after %v for address %s", timeout, addr)
		default:
			conn, err := net.Dial("tcp", addr)
			if err == nil {
				conn.Close()
				return nil
			}
			time.Sleep(50 * time.Millisecond) // Wait a bit before retrying
		}
	}
}

// Helper to get a free TCP address
// mockServiceManager is a mock implementation of the service_manager interface for testing.
type mockServiceManager struct {
	mu              sync.Mutex
	connects        map[string]*conn.Conn
	disconnects     map[string]error
	messages        map[string][][]byte
	onConnectErr    error
	onDisconnectErr error
	onMessageErr    error
	closeCalled     bool
	connectCh       chan struct{}
}

func newMockServiceManager() *mockServiceManager {
	return &mockServiceManager{
		connects:    make(map[string]*conn.Conn),
		disconnects: make(map[string]error),
		messages:    make(map[string][][]byte),
		connectCh:   make(chan struct{}, 100),
	}
}

func (m *mockServiceManager) OnConnect(sid string, c *conn.Conn) error {
	if m.onConnectErr != nil {
		return m.onConnectErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connects[sid] = c
	m.connectCh <- struct{}{}
	return nil
}

func (m *mockServiceManager) OnMessage(sid string, data []byte) error {
	if m.onMessageErr != nil {
		return m.onMessageErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages[sid] = append(m.messages[sid], data)
	// Echo message back for some tests
	if c, ok := m.connects[sid]; ok {
		return c.Send(data)
	}
	return nil
}

func (m *mockServiceManager) OnDisconnect(sid string, err error) error {
	if m.onDisconnectErr != nil {
		return m.onDisconnectErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnects[sid] = err
	return nil
}

func (m *mockServiceManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalled = true
	for _, c := range m.connects {
		c.Close()
	}
	return nil
}

func (m *mockServiceManager) ConnectionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.connects)
}

func TestNewServer(t *testing.T) {
	mgr := newMockServiceManager()
	s := New(context.Background(), mgr)
	require.NotNil(t, s)
	assert.NotNil(t, s.opt)
	assert.Equal(t, mgr, s.mgr)
}

func TestServerListenAndClose(t *testing.T) {
	mgr := newMockServiceManager()
	ctx := context.Background()
	s := New(ctx, mgr)
	addr := "127.0.0.1:0" // Use port 0 to let the OS choose a free port

	var listener net.Listener
	var err error
	// Retry listening to avoid port conflicts in CI
	for i := 0; i < 3; i++ {
		listener, err = net.Listen("tcp", addr)
		if err == nil {
			addr = listener.Addr().String()
			listener.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NoError(t, err, "could not find a free port to listen on")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.Listen(addr)
	}()

	// Give the server a moment to start listening
	time.Sleep(100 * time.Millisecond)

	// Attempt to connect to ensure the server is up
	clientConn, err := net.Dial("tcp", addr)
	require.NoError(t, err, "server should be listening")
	clientConn.Close()

	// Now close the server
	err = s.Close()
	assert.NoError(t, err)

	wg.Wait() // Wait for the Listen goroutine to exit

	assert.True(t, mgr.closeCalled, "service manager's Close should be called")
}

func TestServerAuthentication(t *testing.T) {
	t.Parallel()
	secret := "my-secret-key"

	mgr := newMockServiceManager()
	ctx := context.Background()
	s := New(ctx, mgr, Options().SetSecret(secret))

	addr := "127.0.0.1:8080" // Use a fixed port for simplicity

	var serverWg sync.WaitGroup
	serverWg.Add(1)
	go func() {
		defer serverWg.Done()
		if err := s.Listen(addr); err != nil {
			t.Errorf("TestServerAuthentication server.Listen exited with error: %v", err)
		}
	}()

	// Wait for the server to be ready to accept connections
	require.NoError(t, pingServer(addr, 5*time.Second), "server did not become ready for authentication test")

	defer func() {
		s.Close()
		serverWg.Wait()
	}()

	t.Run("Successful Authentication", func(t *testing.T) {
		t.Parallel()
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err, "client failed to dial server for successful authentication")
		defer conn.Close()

		// Send the correct secret
		_, err = conn.Write([]byte(secret))
		require.NoError(t, err)
		time.Sleep(50 * time.Millisecond) // Give server time to process and respond

		// Expect auth success byte
		response := make([]byte, 1)
		_, err = conn.Read(response)
		require.NoError(t, err)
		assert.Equal(t, byte(0x0C), response[0])
	})

	t.Run("Failed Authentication", func(t *testing.T) {
		t.Parallel()
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err, "client failed to dial server for failed authentication")
		defer conn.Close()

		// Send the wrong secret
		_, err = conn.Write([]byte("wrong-secret"))
		require.NoError(t, err)
		time.Sleep(50 * time.Millisecond) // Give server time to process and respond

		// Expect auth fail byte
		response := make([]byte, 1)
		_, err = conn.Read(response)
		require.NoError(t, err)
		assert.Equal(t, byte(0x00), response[0])

		// The connection should be closed by the server immediately after
		_, err = conn.Read(make([]byte, 1))
		assert.Error(t, err, "connection should be closed after failed auth")
	})
}



func TestServerMultipleListeners(t *testing.T) {
	t.Parallel()
	mgr := newMockServiceManager()
	ctx := context.Background()
	s := New(ctx, mgr)

	addr1 := "127.0.0.1:8081"
	addr2 := "127.0.0.1:8082"

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.Listen(addr1, addr2); err != nil {
			t.Errorf("TestServerMultipleListeners server.Listen exited with error: %v", err)
		}
	}()
	defer func() {
		s.Close()
		wg.Wait()
	}()

	// Wait for both servers to be ready
	require.NoError(t, pingServer(addr1, 5*time.Second))
	require.NoError(t, pingServer(addr2, 5*time.Second))

	// Test connection to the first address
	conn1, err := net.Dial("tcp", addr1)
	require.NoError(t, err)
	conn1.Close()
	time.Sleep(100 * time.Millisecond)

	// Test connection to the second address
	conn2, err := net.Dial("tcp", addr2)
	require.NoError(t, err)
	conn2.Close()
	time.Sleep(100 * time.Millisecond)
}

func TestOptionMerging(t *testing.T) {
	t.Run("Default options", func(t *testing.T) {
		opt := Options()
		assert.NotNil(t, opt)
		assert.Nil(t, opt.Secret)
		assert.Nil(t, opt.ReadDeadline)
	})

	t.Run("Merge with Secret", func(t *testing.T) {
		base := Options()
		secretVal := "test-secret"
		delta := Options().SetSecret(secretVal)
		
		merged := base.Merge(delta)
		require.NotNil(t, merged)
		assert.NotNil(t, merged.Secret)
		assert.Equal(t, secretVal, *merged.Secret)
	})

	t.Run("Merge with conn.Option fields", func(t *testing.T) {
		base := Options()
		readDeadline := 5 * time.Second
		sendChanSize := 100
		
		delta := Options().SetReadDeadline(readDeadline).SetSendChanSize(sendChanSize)

		merged := base.Merge(delta)
		require.NotNil(t, merged)
		assert.NotNil(t, merged.ReadDeadline)
		assert.Equal(t, readDeadline, *merged.ReadDeadline)
		assert.NotNil(t, merged.SendChanSize)
		assert.Equal(t, sendChanSize, *merged.SendChanSize)
	})

	t.Run("Merge multiple options", func(t *testing.T) {
		base := Options()
		
		secretVal1 := "secret1"
		opt1 := Options().SetSecret(secretVal1)

		readDeadline := 10 * time.Second
		opt2 := Options().SetReadDeadline(readDeadline)

		sendChanSize := 200
		opt3 := Options().SetSendChanSize(sendChanSize)

		merged := base.Merge(opt1, opt2, opt3)
		require.NotNil(t, merged)
		assert.NotNil(t, merged.Secret)
		assert.Equal(t, secretVal1, *merged.Secret) // Opt1 should override base
		
		assert.NotNil(t, merged.ReadDeadline)
		assert.Equal(t, readDeadline, *merged.ReadDeadline)

		assert.NotNil(t, merged.SendChanSize)
		assert.Equal(t, sendChanSize, *merged.SendChanSize)
	})

	t.Run("Merge nil delta option", func(t *testing.T) {
		base := Options().SetSecret("initial").SetReadDeadline(1 * time.Second)
		initialSecret := *base.Secret
		initialReadDeadline := *base.ReadDeadline

		merged := base.Merge(nil)
		require.NotNil(t, merged)
		assert.NotNil(t, merged.Secret)
		assert.Equal(t, initialSecret, *merged.Secret)
		assert.NotNil(t, merged.ReadDeadline)
		assert.Equal(t, initialReadDeadline, *merged.ReadDeadline)
	})
}

func TestServerWithConnOptions(t *testing.T) {
	t.Run("Verify ReadDeadline option", func(t *testing.T) {
		deadline := 10 * time.Second
		        options := Options().SetReadDeadline(deadline)
		        mgr := newMockServiceManager()
		        s := New(context.Background(), mgr, options)
		require.NotNil(t, s.opt)
		assert.NotNil(t, s.opt.ReadDeadline)
		assert.Equal(t, deadline, *s.opt.ReadDeadline)
	})

	t.Run("Verify SendChanSize option", func(t *testing.T) {
		size := 256
		options := Options().SetSendChanSize(size)
		mgr := newMockServiceManager()
		s := New(context.Background(), mgr, options)

		require.NotNil(t, s.opt)
		assert.NotNil(t, s.opt.SendChanSize)
		assert.Equal(t, size, *s.opt.SendChanSize)
	})

	t.Run("Verify HeartInterval option", func(t *testing.T) {
		interval := 30 * time.Second
		options := Options().SetHeartInterval(interval)
		mgr := newMockServiceManager()
		s := New(context.Background(), mgr, options)

		require.NotNil(t, s.opt)
		assert.NotNil(t, s.opt.HeartInterval)
		assert.Equal(t, interval, *s.opt.HeartInterval)
	})

	t.Run("Verify MaxFrameSize option", func(t *testing.T) {
		maxSize := uint64(4096)
		options := Options().SetMaxFrameSize(maxSize)
		mgr := newMockServiceManager()
		s := New(context.Background(), mgr, options)

		require.NotNil(t, s.opt)
		assert.NotNil(t, s.opt.MaxFrameSize)
		assert.Equal(t, maxSize, *s.opt.MaxFrameSize)
	})

	t.Run("Verify multiple conn.Option fields", func(t *testing.T) {
		readDeadline := 5 * time.Second
		sendChanSize := 128
		options := Options().SetReadDeadline(readDeadline).SetSendChanSize(sendChanSize)
		mgr := newMockServiceManager()
		s := New(context.Background(), mgr, options)

		require.NotNil(t, s.opt)
		assert.NotNil(t, s.opt.ReadDeadline)
		assert.Equal(t, readDeadline, *s.opt.ReadDeadline)
		assert.NotNil(t, s.opt.SendChanSize)
		assert.Equal(t, sendChanSize, *s.opt.SendChanSize)
	})
}

