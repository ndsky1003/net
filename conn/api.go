package conn

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// WARNING: 非线程安全
// 就是链接建立之前使用 --start
func (this *Conn) Write(data []byte, opts ...*Option) (err error) {
	return this.write(flag_msg, data, opts...)
}

// WARNING: 非线程安全
// 就是链接建立之前使用
func (this *Conn) Flush() error {
	return this.w.Flush()
}

// WARNING: 非线程安全
// 就是链接建立之前使用
func (this *Conn) Read(opts ...*Option) (data []byte, err error) {
	var flag byte
	flag, data, err = this.read(opts...)
	if err != nil {
		return nil, err
	}
	if flag != flag_msg {
		// 如果收到的不是普通消息（例如收到了 Ping），在握手阶段应当视为协议错误或忽略
		return nil, fmt.Errorf("unexpected flag during handshake: %d", flag)
	}
	return
}

// 就是链接建立之前使用 --end

// WARNING: 线程安全
func (this *Conn) Send(ctx context.Context, data []byte, opts ...*Option) (err error) {
	if this.closed.Load() {
		return fmt.Errorf("connection closed")
	}

	opt := Options().Merge(this.opt).Merge(opts...)
	msg := &msg{
		flag: flag_msg,
		data: data,
		opt:  opt,
	}

	if timeout := opt.SendChanTimeout; timeout != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}
	select {
	case <-this.ctx.Done():
		return fmt.Errorf("connection closed")
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("send timeout")
		}
		return fmt.Errorf("send cancelled:%w", ctx.Err())
	case this.sendChan <- msg:
	}
	return nil
}

func (this *Conn) Serve() error {
	var wg sync.WaitGroup
	wg.Add(2)

	// readPump goroutine (读协程)
	go func() {
		defer wg.Done()
		err := this.readPump() // 运行读协程，获取其错误
		if err != nil {
			this.l.Lock() // 记录第一个错误
			if this.closeErr == nil {
				this.closeErr = err
			}
			this.l.Unlock()
		}
		this.Close() // 触发连接关闭 (幂等操作)
	}()

	// writePump goroutine (写协程)
	go func() {
		defer wg.Done()
		err := this.writePump() // 运行写协程，获取其错误
		if err != nil {
			this.l.Lock() // 记录第一个错误
			if this.closeErr == nil {
				this.closeErr = err
			}
			this.l.Unlock()
		}
		this.Close() // 触发连接关闭 (幂等操作)
	}()

	// 等待 readPump 和 writePump 都完成。
	wg.Wait()

	// Serve 返回记录的关闭错误。
	this.l.Lock()
	err := this.closeErr
	this.l.Unlock()
	return err
}

func (this *Conn) Close() error {
	if !this.closed.CompareAndSwap(false, true) {
		this.l.Lock()
		err := this.closeErr
		this.l.Unlock()
		return err
	}
	this.cancel() // 调用 cancel 替代 close(done)
	// 2. 中断底层 net.Conn 上任何阻塞的读/写操作。
	//    这有助于 readPump 和 writePump (如果阻塞在 Write) 快速退出。
	this.Conn.SetReadDeadline(time.Now()) // 中断阻塞的读操作
	this.Conn.Close()

	if f := this.opt.OnCloseCallbackDiscardMsg; f != nil {
		var discardedMsgs [][]byte
		for msg := range this.sendChan {
			discardedMsgs = append(discardedMsgs, msg.data)
		}
		f(discardedMsgs)
		this.opt.SetOnCloseCallbackDiscardMsg(nil) // 释放回调
	}

	this.l.Lock()
	err := this.closeErr
	this.l.Unlock()
	return err
}
