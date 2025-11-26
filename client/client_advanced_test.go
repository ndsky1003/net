package client_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ndsky1003/net/client"
	"github.com/ndsky1003/net/conn"
	"github.com/ndsky1003/net/server"
)

// TestClientAuthenticationFailure 验证认证失败的场景
func TestClientAuthenticationFailure(t *testing.T) {
	// 1. 启动带密钥的服务器
	serverAddr := "127.0.0.1:8083"
	serverMgr := newTestServiceManager()
	serverInst := server.New(serverMgr, server.Options().SetSecret("mysecret"))

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- serverInst.Listen(serverAddr)
	}()
	time.Sleep(100 * time.Millisecond)
	defer func() {
		serverInst.Close()
		<-serverErrCh
	}()

	// 2. 创建使用错误密钥的客户端
	authFailedCh := make(chan error, 1)
	clientHandler := &echoHandler{}
	clientOpts := client.Options().SetHandler(clientHandler).SetSecret("wrongsecret").SetOnAuthFailed(func(err error) {
		authFailedCh <- err
	})
	c, err := client.Dial("testClient", serverAddr, clientOpts)
	if err != nil {
		t.Fatalf("Client Dial failed initially: %v", err)
	}
	defer c.Close()

	// 3. 验证认证失败
	select {
	case authErr := <-authFailedCh:
		t.Logf("Received expected authentication failure: %v", authErr)
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Timed out waiting for authentication failure")
	}
}

// TestClientStress 验证压力测试场景
func TestClientStress(t *testing.T) {
	// 1. 启动服务器
	serverAddr := "127.0.0.1:8084"
	serverMgr := newTestServiceManager()
	serverInst := server.New(serverMgr, server.Options().SetSecret("mysecret"))

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- serverInst.Listen(serverAddr)
	}()
	time.Sleep(100 * time.Millisecond)
	defer func() {
		serverInst.Close()
		<-serverErrCh
	}()

	// 2. 创建客户端
	clientHandler := &echoHandler{}
	clientOpts := client.Options().SetHandler(clientHandler).SetSecret("mysecret")
	clientInst, err := client.Dial("testClient", serverAddr, clientOpts)
	if err != nil {
		t.Fatalf("Client Dial failed: %v", err)
	}
	defer clientInst.Close()

	// 等待连接成功
	for i := 0; i < 50 && !clientInst.IsConnected(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !clientInst.IsConnected() {
		t.Fatal("Client failed to connect to server.")
	}

	// 3. 发送和接收大量消息
	const messageCount = 1000
	for i := 0; i < messageCount; i++ {
		sentMessage := []byte(fmt.Sprintf("message %d", i))
		if err := clientInst.Send(sentMessage); err != nil {
			t.Fatalf("Client failed to send message %d: %v", i, err)
		}
		receivedMessage, err := clientHandler.WaitForMessage(1 * time.Second)
		if err != nil {
			t.Fatalf("Client failed to receive echoed message %d: %v", i, err)
		}
		if string(receivedMessage) != string(sentMessage) {
			t.Fatalf("Message mismatch for message %d: sent '%s', received '%s'", i, sentMessage, receivedMessage)
		}
	}
	t.Logf("Successfully sent and received %d messages.", messageCount)
}

// TestClientConcurrency 验证并发场景
func TestClientConcurrency(t *testing.T) {
	// 1. 启动服务器
	serverAddr := "127.0.0.1:8085"
	serverMgr := newTestSvcMgrWithEcho()
	serverInst := server.New(serverMgr, server.Options().SetSecret("mysecret"))

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- serverInst.Listen(serverAddr)
	}()
	time.Sleep(100 * time.Millisecond)
	defer func() {
		serverInst.Close()
		<-serverErrCh
	}()

	// 2. 并发创建和测试客户端
	const clientCount = 100
	var wg sync.WaitGroup
	wg.Add(clientCount)

	for i := 0; i < clientCount; i++ {
		go func(clientID int) {
			defer wg.Done()

			clientHandler := &echoHandler{}
			clientOpts := client.Options().SetHandler(clientHandler).SetSecret("mysecret")
			clientInst, err := client.Dial(fmt.Sprintf("client-%d", clientID), serverAddr, clientOpts)
			if err != nil {
				t.Errorf("Client %d Dial failed: %v", clientID, err)
				return
			}
			defer clientInst.Close()

			for j := 0; j < 50 && !clientInst.IsConnected(); j++ {
				time.Sleep(100 * time.Millisecond)
			}
			if !clientInst.IsConnected() {
				t.Errorf("Client %d failed to connect to server.", clientID)
				return
			}

			sentMessage := []byte(fmt.Sprintf("Hello from client %d", clientID))
			if err := clientInst.Send(sentMessage); err != nil {
				t.Errorf("Client %d failed to send message: %v", clientID, err)
				return
			}

			receivedMessage, err := clientHandler.WaitForMessage(2 * time.Second)
			if err != nil {
				t.Errorf("Client %d failed to receive echoed message: %v", clientID, err)
				return
			}

			if string(receivedMessage) != string(sentMessage) {
				t.Errorf("Message mismatch for client %d: sent '%s', received '%s'", clientID, sentMessage, receivedMessage)
			}
		}(i)
	}

	wg.Wait()
	t.Logf("Concurrency test with %d clients completed.", clientCount)
}

// newTestSvcMgrWithEcho is a helper to create a service manager that echoes messages.
// This is needed because the existing testServiceManager has race conditions when used concurrently.
func newTestSvcMgrWithEcho() *echoServiceManager {
	return &echoServiceManager{
		conns: make(map[string]*conn.Conn),
	}
}

type echoServiceManager struct {
	mu    sync.Mutex
	conns map[string]*conn.Conn
}

func (m *echoServiceManager) OnConnect(sid string, c *conn.Conn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns[sid] = c
	return nil
}

func (m *echoServiceManager) OnMessage(sid string, data []byte) error {
	m.mu.Lock()
	c, ok := m.conns[sid]
	m.mu.Unlock()

	if ok {
		// Send can be slow, so we do it outside the lock.
		if err := c.Send(data); err != nil {
			fmt.Printf("server failed to echo message to %s: %v\n", sid, err)
		}
	}
	return nil
}

func (m *echoServiceManager) OnDisconnect(sid string, err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conns, sid)
	return nil
}

func (m *echoServiceManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.Close()
	}
	return nil
}
