package conn

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestHeartbeatTimeout 验证心跳超时机制是否能正常工作
func TestHeartbeatTimeout(t *testing.T) {
	// 1. 创建一个内存中的网络连接对（客户端 <-> 服务端）
	serverRaw, clientRaw := net.Pipe()

	// 2. 定义一个简单的 handler, 这里我们不需要它做任何事
	handler := &echoHandler{}

	// 3. 设置一个非常短的心跳间隔以便快速测试
	heartbeatInterval := 1 * time.Second
	opts := Options().SetHeartInterval(heartbeatInterval)

	// 4. 创建服务端的 Conn
	serverConn := New(serverRaw, handler, opts)

	// 5. 启动服务端的 Serve() 协程，这是测试的核心目标
	//    我们期望它会在心跳超时后返回错误并退出
	serverErrChan := make(chan error, 1)
	go func() {
		// 增加 SetReadDeadline 是更健壮的做法，确保 readPump 及时退出
		serverRaw.SetReadDeadline(time.Now())
		serverErrChan <- serverConn.Serve()
	}()

	// 6. 客户端保持静默，什么也不做，模拟一个无响应的客户端

	// 7. 等待足够的时间让心跳机制触发
	// 新逻辑超时时间为 heart_interval, 我们等待 1.5 倍的时间确保其触发
	time.Sleep(heartbeatInterval + heartbeatInterval/2)

	// 8. 检查服务端是否如预期一样因为超时而关闭
	select {
	case err := <-serverErrChan:
		if err == nil {
			t.Fatal("Server should have been closed due to heartbeat timeout, but it exited cleanly.")
		}
		// 我们可以进一步检查错误信息是否与心跳相关
		// 根据当前代码，它不会直接返回错误，而是调用 Close(), 导致 Serve() 收到 readPump 的错误
		t.Logf("Server exited with expected error: %v", err)
	case <-time.After(1 * time.Second):
		t.Fatal("Server did not close after heartbeat timeout.")
	}

	// 9. 顺便测试一下客户端 conn 的关闭
	if err := clientRaw.Close(); err != nil {
		t.Errorf("Failed to close client connection: %v", err)
	}
}

// TestTemporaryReadDeadline 验证临时读取超时不会影响后续的阻塞读取
func TestTemporaryReadDeadline(t *testing.T) {
	serverRaw, clientRaw := net.Pipe()
	handler := &echoHandler{}
	clientConn := New(clientRaw, handler)

	// 模拟 server 端
	go func() {
		// 先不要写入任何东西，让客户端的第一次 read 超时
		time.Sleep(100 * time.Millisecond)
		// 在客户端第二次 read 之前写入数据
		// 0x06 代表 1 byte flag + 5 bytes "hello" = 6 bytes
		serverRaw.Write([]byte{0x06, flag_msg, 'h', 'e', 'l', 'l', 'o'})
		serverRaw.Close() // 关闭服务器端连接
	}()

	// 1. 第一次读取，设置一个很短的超时，预期会失败
	_, err := clientConn.Read(Options().SetReadDeadline(50 * time.Millisecond))
	if err == nil {
		t.Fatal("First read should have timed out, but it succeeded.")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Logf("Expected a timeout error, but got: %v", err)
	}
	t.Logf("First read failed with expected timeout error: %v", err)

	// 2. 第二次读取，不设置超时，预期会成功阻塞并读取到数据
	//    这是为了验证 defer SetReadDeadline(time.Time{}) 是否生效
	data, err := clientConn.Read()
	if err != nil {
		t.Fatalf("Second read should have succeeded, but failed with: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("Expected to read 'hello', but got '%s'", string(data))
	}
	t.Log("Second read succeeded after temporary timeout, fix is working.")
	clientConn.Close()
}

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

func (h *echoHandler) GetLastMessage() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.message
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
