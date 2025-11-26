package server

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ndsky1003/net/conn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
}

func newMockServiceManager() *mockServiceManager {
	return &mockServiceManager{
		connects:    make(map[string]*conn.Conn),
		disconnects: make(map[string]error),
		messages:    make(map[string][][]byte),
	}
}

func (m *mockServiceManager) OnConnect(sid string, c *conn.Conn) error {
	if m.onConnectErr != nil {
		return m.onConnectErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connects[sid] = c
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
	s := New(mgr)
	require.NotNil(t, s)
	assert.NotNil(t, s.opt)
	assert.Equal(t, mgr, s.mgr)
}

func TestServerListenAndClose(t *testing.T) {
	mgr := newMockServiceManager()
	s := New(mgr)
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

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Listen(addr)
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

	// The Listen goroutine should exit gracefully
	select {
	case listenErr := <-errCh:
		assert.NoError(t, listenErr, "Listen should exit without error on Close")
	case <-time.After(1 * time.Second):
		t.Fatal("server did not shut down gracefully")
	}

	assert.True(t, mgr.closeCalled, "service manager's Close should be called")
}

func TestServerAuthentication(t *testing.T) {
	secret := "my-secret-key"
	mgr := newMockServiceManager()
	s := New(mgr, Options().SetSecret(secret))
	addr := "127.0.0.1:0"

	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	addr = listener.Addr().String()
	listener.Close()

	go s.Listen(addr)
	defer s.Close()
	time.Sleep(100 * time.Millisecond)

	t.Run("Successful Authentication", func(t *testing.T) {
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()

		// Send the correct secret
		_, err = conn.Write([]byte(secret))
		require.NoError(t, err)

		// Expect auth success byte
		response := make([]byte, 1)
		_, err = conn.Read(response)
		require.NoError(t, err)
		assert.Equal(t, byte(0x0C), response[0])

		// Check if the connection was registered in the manager
		time.Sleep(100 * time.Millisecond) // Give manager time to process connection
		assert.Equal(t, 1, mgr.ConnectionCount())
		
		// Clean up for the next test
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("Failed Authentication", func(t *testing.T) {
		conn, err := net.Dial("tcp", addr)
		require.NoError(t, err)
		defer conn.Close()

		// Send the wrong secret
		_, err = conn.Write([]byte("wrong-secret"))
		require.NoError(t, err)

		// Expect auth fail byte
		response := make([]byte, 1)
		_, err = conn.Read(response)
		require.NoError(t, err)
		assert.Equal(t, byte(0x00), response[0])

		// The connection should be closed by the server immediately after
		_, err = conn.Read(make([]byte, 1))
		assert.Error(t, err, "connection should be closed after failed auth")
		
		// Ensure the connection was NOT registered in the manager
		assert.Equal(t, 0, mgr.ConnectionCount())
	})
}

func TestServerMultipleListeners(t *testing.T) {
	mgr := newMockServiceManager()
	s := New(mgr)

	// Get two free ports
	addr1, l1 := getFreeAddr(t)
	l1.Close()
	addr2, l2 := getFreeAddr(t)
	l2.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Listen(addr1, addr2)
	}()
	defer s.Close()
	time.Sleep(100 * time.Millisecond)

	// Test connection to the first address
	conn1, err := net.Dial("tcp", addr1)
	require.NoError(t, err)
	conn1.Close()

	// Test connection to the second address
	conn2, err := net.Dial("tcp", addr2)
	require.NoError(t, err)
	conn2.Close()

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 2, mgr.ConnectionCount(), "should have accepted two connections")
}

// Helper to get a free TCP address
func getFreeAddr(t *testing.T) (string, net.Listener) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return l.Addr().String(), l
}
