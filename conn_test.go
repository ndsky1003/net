package net

import (
	"context"
	"encoding/binary"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// handlerFunc 把 func([]byte) error 适配成 Handler，方便测试。
type handlerFunc func([]byte) error

func (f handlerFunc) HandleMsg(b []byte) error { return f(b) }

// TestEcho 验证最基本的一收一发。
// 注意：net.Pipe 是同步无缓冲的，写端会阻塞到读端读完，
// 所以两端都要先 Serve 起来再 Write，否则 writePump 会卡在 pipe 写。
func TestEcho(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	received := make(chan []byte, 1)
	srv := New(context.Background(), server, handlerFunc(func(body []byte) error {
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
			c := New(context.Background(), conn, handlerFunc(func(body []byte) error {
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
	srv := New(context.Background(), server, handlerFunc(func(body []byte) error {
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
