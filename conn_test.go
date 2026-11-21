package net

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// handlerFunc 把 func(int, []byte) error 适配成 Handler，方便测试。
type handlerFunc func(remain int, body []byte) error

func (f handlerFunc) HandleMsg(remain int, body []byte) error { return f(remain, body) }

// TestEcho 验证最基本的一收一发。
// 注意：net.Pipe 是同步无缓冲的，写端会阻塞到读端读完，
// 所以两端都要先 Serve 起来再 Write，否则 writePump 会卡在 pipe 写。
func TestEcho(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	received := make(chan []byte, 1)
	srv := New(context.Background(), server, handlerFunc(func(remain int, body []byte) error {
		received <- body
		return nil
	}))
	go srv.Serve()

	cli := New(context.Background(), client, nil)
	go cli.Serve()

	if err := cli.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case body := <-received:
		if string(body) != "hello" {
			t.Fatalf("got %q, want %q", body, "hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for echo")
	}
}

// TestClose 验证主动关闭幂等，且 Err 返回 ErrConnClosed。
func TestClose(t *testing.T) {
	_, client := net.Pipe()
	c := New(context.Background(), client, nil)
	go c.Serve()

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !c.IsClosed() {
		t.Fatal("expected closed")
	}
	if c.Err() != ErrConnClosed {
		t.Fatalf("Err() = %v, want ErrConnClosed", c.Err())
	}
	// 幂等：第二次 Close 返回 nil，closeErr 不被覆盖
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestCancelClosesConn 验证 cancel 传入的 ctx 能立即关闭连接。
// writePump 从 ctx.Done() 退出并返回 ctx.Err()，触发 CloseWithError -> conn.Close，
// 从而叫醒阻塞中的 readPump。
func TestCancelClosesConn(t *testing.T) {
	_, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	c := New(ctx, client, nil)

	done := make(chan error, 1)
	go func() { done <- c.Serve() }()
	time.Sleep(10 * time.Millisecond) // 等 Serve 起泵

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve 应返回非 nil 错误")
		}
		if !c.IsClosed() {
			t.Fatal("cancel 后连接应已关闭")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel 后 Serve 未返回")
	}
}

// TestWriteTooLarge 验证超长帧在入队前就被拒绝，不关闭连接。
func TestWriteTooLarge(t *testing.T) {
	_, client := net.Pipe()
	c := New(context.Background(), client, nil)

	big := make([]byte, 64<<10+1)
	if err := c.Write(big); err == nil {
		t.Fatal("expected error for oversized frame")
	}
	if c.IsClosed() {
		t.Fatal("oversized frame 不应关闭连接")
	}
}

// TestNewFrameZeroCopy 验证组帧不复制 body，直接引用调用方数据。
// 这正是 Write 返回后调用方在 writev 完成前不能复用 data 的原因。
func TestNewFrameZeroCopy(t *testing.T) {
	_, client := net.Pipe()
	defer client.Close()
	c := New(context.Background(), client, nil)

	data := []byte("hello")
	f, err := c.newFrame(flag_msg, data)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	if &f.body[0] != &data[0] {
		t.Fatal("frame body 应直接引用调用方 data，不复制")
	}
}

// TestFrameHeader 验证帧头字节布局：[magic 1B][flag 1B][len 4B BigEndian]。
func TestFrameHeader(t *testing.T) {
	_, client := net.Pipe()
	defer client.Close()
	c := New(context.Background(), client, nil)

	f, err := c.newFrame(flag_ping, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	if f.hdr[0] != magic {
		t.Fatalf("magic = %#x, want %#x", f.hdr[0], magic)
	}
	if f.hdr[1] != flag_ping {
		t.Fatalf("flag = %#x, want %#x", f.hdr[1], flag_ping)
	}
	if n := binary.BigEndian.Uint32(f.hdr[2:]); n != 0 {
		t.Fatalf("size = %d, want 0", n)
	}
}

// BenchmarkConnQPS 测真实 TCP 回环下单连接的端到端帧吞吐。
// 每帧 1 字节 body，计数到 b.N 即代表全部送达并处理完成。
func BenchmarkConnQPS(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer ln.Close()

	var received atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// 大 MsgChanSize 避免高速小帧下 msgChan 满而断连，专测纯吞吐上限
			c := New(context.Background(), conn, handlerFunc(func(remain int, body []byte) error {
				received.Add(1)
				return nil
			}), Options().SetMsgChanSize(1<<20))
			go c.Serve()
		}
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	cli := New(context.Background(), conn, nil)
	go cli.Serve()

	payload := []byte("x")
	b.SetBytes(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
	for received.Load() < int64(b.N) {
		runtime.Gosched()
	}
	b.StopTimer()
}

// BenchmarkConnQPSPipe 测 net.Pipe（纯内存、同步）下单连接端到端吞吐，
// 作为协议层在无内核网络开销下的上限参考。
func BenchmarkConnQPSPipe(b *testing.B) {
	server, client := net.Pipe()
	defer client.Close()

	var received atomic.Int64
	srv := New(context.Background(), server, handlerFunc(func(remain int, body []byte) error {
		received.Add(1)
		return nil
	}))
	go srv.Serve()

	cli := New(context.Background(), client, nil)
	go cli.Serve()

	payload := []byte("x")
	b.SetBytes(1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cli.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
	for received.Load() < int64(b.N) {
		runtime.Gosched()
	}
	b.StopTimer()
}

// TestLargeFrameRemain 验证大帧的流式分块交付：一个超过 readBlockSize 的 body
// 会被拆成多块依次回调，remain 递减，最后一块 remain == 0 表示本帧结束。
func TestLargeFrameRemain(t *testing.T) {
	const frameSize = 1 << 20 // 1MB，大于默认 MaxFrameSize(64KB) 与 readBlockSize
	payload := make([]byte, frameSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Handler 由唯一的 msgWorker 串行调用，累积时无需加锁；
	// close(done) 与 <-done 之间由 channel 建立 happens-before，主 goroutine 读 chunks 安全。
	var buf bytes.Buffer
	var chunks, lastRemain int
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		c := New(context.Background(), conn, handlerFunc(func(remain int, body []byte) error {
			buf.Write(body)
			chunks++
			lastRemain = remain
			if remain == 0 {
				close(done)
			}
			return nil
		}), Options().SetMaxFrameSize(frameSize)) // 接收端也放开单帧上限
		c.Serve()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := New(context.Background(), conn, nil, Options().SetMaxFrameSize(frameSize)) // 发送端放开上限
	go c.Serve()

	// 发一个完整的大帧，不拆帧；zero-copy 下 payload 只读，安全。
	if err := c.Write(payload); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out delivering frame")
	}

	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatal("delivered payload mismatch")
	}
	if chunks < 2 {
		t.Fatalf("expected multiple chunks, got %d", chunks)
	}
	if lastRemain != 0 {
		t.Fatalf("last remain = %d, want 0", lastRemain)
	}

	c.Close()
}

// TestMessageSizes 覆盖分块逻辑的边界：空消息、单块、以及越过 readBlockSize 的多块，
// 校验字节数、分块数和最后一个 remain 都正确。
func TestMessageSizes(t *testing.T) {
	const rb = 64 << 10
	cases := []struct {
		name   string
		size   int
		chunks int
	}{
		{"empty", 0, 1},
		{"one byte", 1, 1},
		{"exactly readBlockSize", rb, 1},
		{"readBlockSize plus one", rb + 1, 2},
		{"two blocks", 2 * rb, 2},
		{"two blocks plus one", 2*rb + 1, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer client.Close()

			payload := make([]byte, tc.size)
			for i := range payload {
				payload[i] = byte(i)
			}

			var total, chunks int
			var lastRemain int
			done := make(chan struct{})
			go func() {
				c := New(context.Background(), server, handlerFunc(func(remain int, body []byte) error {
					total += len(body)
					chunks++
					lastRemain = remain
					if remain == 0 {
						close(done)
					}
					return nil
				}), Options().SetMaxFrameSize(1<<20))
				c.Serve()
			}()

			c := New(context.Background(), client, nil, Options().SetMaxFrameSize(1<<20))
			go c.Serve()

			if err := c.Write(payload); err != nil {
				t.Fatal(err)
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("timed out")
			}

			if total != tc.size {
				t.Fatalf("total = %d, want %d", total, tc.size)
			}
			if chunks != tc.chunks {
				t.Fatalf("chunks = %d, want %d", chunks, tc.chunks)
			}
			if lastRemain != 0 {
				t.Fatalf("last remain = %d, want 0", lastRemain)
			}
		})
	}
}

// TestWriteReader 验证 WriteReader 从 io.Reader 流式发送：发送端分块读入，
// 接收端仍按分块回调，内容一致。
func TestWriteReader(t *testing.T) {
	const size = 1 << 20 // 1MB
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		c := New(context.Background(), conn, handlerFunc(func(remain int, body []byte) error {
			buf.Write(body)
			if remain == 0 {
				close(done)
			}
			return nil
		}), Options().SetMaxFrameSize(size))
		c.Serve()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := New(context.Background(), conn, nil, Options().SetMaxFrameSize(size))
	go c.Serve()

	if err := c.WriteReader(bytes.NewReader(payload), size); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	if !bytes.Equal(buf.Bytes(), payload) {
		t.Fatal("payload mismatch")
	}

	c.Close()
}

// TestWriteReaderMixed 验证 Write 与 WriteReader 混合发送时的顺序：
// 流式帧不攒批，但必须按 sendChan 入队顺序写出，不被后续帧插队。
func TestWriteReaderMixed(t *testing.T) {
	const size = 1 << 20
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}
	want := append([]byte("a"), payload...)
	want = append(want, 'b')

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		c := New(context.Background(), conn, handlerFunc(func(remain int, body []byte) error {
			buf.Write(body)
			if buf.Len() >= len(want) {
				select {
				case <-done:
				default:
					close(done)
				}
			}
			return nil
		}), Options().SetMaxFrameSize(size))
		c.Serve()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := New(context.Background(), conn, nil, Options().SetMaxFrameSize(size))
	go c.Serve()

	if err := c.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteReader(bytes.NewReader(payload), size); err != nil {
		t.Fatal(err)
	}
	if err := c.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatal("mixed order mismatch")
	}

	c.Close()
}

// counterReader 是一个 O(1) 内存的生成器：每次 Read 填满确定性递增字节，
// 不持有整个数据，用于验证 WriteReader 流式发送时发送端确实分块读入。
type counterReader struct {
	n int
}

func (r *counterReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r.n)
		r.n++
	}
	return len(p), nil
}

// TestWriteReader1GB 用生成器 Reader（O(1) 内存）流式发送 1G，接收端只喂 hash，
// 验证发送端无需持有 1G 内存、内容完整，并报告吞吐。
func TestWriteReader1GB(t *testing.T) {
	if os.Getenv("NET_TEST_LARGE") == "" {
		t.Skip("set NET_TEST_LARGE=1 to run 1GB stream")
	}
	const size = 1 << 30 // 1GB

	// 期望 hash：用相同生成器重放一遍（不持有 1G 内存）
	want := sha256.New()
	{
		r := &counterReader{}
		for n := 0; n < size; {
			buf := make([]byte, 64<<10)
			m, err := r.Read(buf)
			want.Write(buf[:m])
			n += m
			if err != nil {
				break
			}
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := sha256.New()
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		c := New(context.Background(), conn, handlerFunc(func(remain int, body []byte) error {
			got.Write(body)
			if remain == 0 {
				close(done)
			}
			return nil
		}), Options().SetMaxFrameSize(size))
		c.Serve()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := New(context.Background(), conn, nil, Options().SetMaxFrameSize(size))
	go c.Serve()

	start := time.Now()
	if err := c.WriteReader(&counterReader{}, size); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("timed out")
	}

	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatal("hash mismatch")
	}
	elapsed := time.Since(start)
	t.Logf("1GB stream in %v, %.0f MB/s", elapsed, float64(size)/elapsed.Seconds()/1e6)

	c.Close()
}

// TestLargeFrame1GB 实测 1G 单帧传输，校验完整性，并报告吞吐。
// 默认跳过（占 1G 内存），设置 NET_TEST_LARGE=1 才运行：
//
//	NET_TEST_LARGE=1 go test -run TestLargeFrame1GB -v
func TestLargeFrame1GB(t *testing.T) {
	if os.Getenv("NET_TEST_LARGE") == "" {
		t.Skip("set NET_TEST_LARGE=1 to run 1GB transfer")
	}
	const frameSize = 1 << 30 // 1GB
	payload := make([]byte, frameSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	want := sha256.Sum256(payload)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// 接收端逐块喂 hash，不累积，避免再占 1G 内存。
	h := sha256.New()
	var chunks int
	done := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		c := New(context.Background(), conn, handlerFunc(func(remain int, body []byte) error {
			h.Write(body)
			chunks++
			if remain == 0 {
				close(done)
			}
			return nil
		}), Options().SetMaxFrameSize(frameSize))
		serverErr <- c.Serve()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := New(context.Background(), conn, nil, Options().SetMaxFrameSize(frameSize))
	clientErr := make(chan error, 1)
	go func() { clientErr <- c.Serve() }()

	start := time.Now()
	if err := c.Write(payload); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		var se, ce error
		select {
		case se = <-serverErr:
		default:
		}
		select {
		case ce = <-clientErr:
		default:
		}
		t.Fatalf("timed out transferring 1GB; chunks=%d serverErr=%v clientErr=%v", chunks, se, ce)
	}

	if !bytes.Equal(h.Sum(nil), want[:]) {
		t.Fatal("hash mismatch")
	}
	elapsed := time.Since(start)
	t.Logf("1GB in %v, %d chunks, %.0f MB/s", elapsed, chunks, float64(frameSize)/elapsed.Seconds()/1e6)

	c.Close()
}
