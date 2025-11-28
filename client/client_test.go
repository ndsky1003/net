package client_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ndsky1003/net/client"
	"github.com/ndsky1003/net/conn"
	"github.com/ndsky1003/net/server"
)

// 辅助结构，用于实现 conn.Handler 接口
type echoHandler struct {
	mu      sync.Mutex
	message []byte
	msgCh   chan []byte
}

func (h *echoHandler) HandleMsg(data []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.message = data
	if h.msgCh != nil {
		h.msgCh <- data
	}
	return nil
}

func (h *echoHandler) WaitForMessage(timeout time.Duration) ([]byte, error) {
	h.msgCh = make(chan []byte, 1)
	select {
	case msg := <-h.msgCh:
		return msg, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for message")
	}
}

// testServiceManager 实现 server.service_manager 接口
type testServiceManager struct {
	mu              sync.Mutex
	onConnectCnt    int
	onDisconnectCnt int
	lastMessage     []byte
	messageCh       chan []byte
	serverConns     map[string]*conn.Conn
}

func newTestServiceManager() *testServiceManager {
	return &testServiceManager{
		messageCh:   make(chan []byte, 1),
		serverConns: make(map[string]*conn.Conn),
	}
}

func (m *testServiceManager) OnConnect(sid string, c *conn.Conn) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConnectCnt++
	m.serverConns[sid] = c
	return nil
}

func (m *testServiceManager) OnMessage(sid string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastMessage = data

	if serverConn, ok := m.serverConns[sid]; ok {
		go func() {
			if err := serverConn.Send(data); err != nil {
				fmt.Printf("server failed to echo message: %v\n", err)
			}
		}()
	}
	return nil
}

func (m *testServiceManager) OnDisconnect(sid string, err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onDisconnectCnt++
	delete(m.serverConns, sid)
	return nil
}

func (m *testServiceManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for sid, c := range m.serverConns {
		if c != nil {
			if err := c.Close(); err != nil {
				errFmt := fmt.Errorf("failed to close connection %s: %w", sid, err)
				errs = append(errs, errFmt)
			}
		}
		delete(m.serverConns, sid)
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing connections: %v", errs)
	}
	return nil
}

// TestClientServerCommunication 验证客户端和服务端之间的通信
func TestClientServerCommunication(t *testing.T) {
	// 1. 启动服务器
	serverAddr := "127.0.0.1:8082" // 使用不同的端口避免与旧测试冲突
	serverMgr := newTestServiceManager()
	serverInst := server.New(context.Background(), serverMgr, server.Options().SetSecret("mysecret"))

	var serverWg sync.WaitGroup
	serverWg.Add(1)
	go func() {
		defer serverWg.Done()
		serverInst.Listen(serverAddr)
	}()
	time.Sleep(100 * time.Millisecond)
	defer func() {
		serverInst.Close()
		serverWg.Wait()
	}()

	// 2. 创建客户端
	clientHandler := &echoHandler{}
	clientOpts := client.Options().SetHandler(clientHandler).SetSecret("mysecret")
	clientInst, err := client.Dial(context.Background(), "testClient", serverAddr, clientOpts)
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

	// 3. 发送和接收消息
	sentMessage := []byte("Hello, world!")
	if err := clientInst.Send(sentMessage); err != nil {
		t.Fatalf("Client failed to send message: %v", err)
	}
	receivedMessage, err := clientHandler.WaitForMessage(1 * time.Second)
	if err != nil {
		t.Fatalf("Client failed to receive echoed message: %v", err)
	}

	// 4. 验证
	if string(receivedMessage) != string(sentMessage) {
		t.Fatalf("Message mismatch: sent '%s', received '%s'", sentMessage, receivedMessage)
	}
	t.Log("Successfully sent and received message.")

	// 5. 关闭
	serverInst.Close()
}
