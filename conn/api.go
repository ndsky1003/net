package conn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrConnectionClosed = errors.New("connection closed")
	ErrSendBufferFull   = errors.New("send buffer full")
)

// WARNING: 非线程安全
// 就是链接建立之前使用 --start
func (this *Conn) Write(data []byte, opts ...*Option) (err error) {
	return this.write(flag_msg, data, opts...)
}

func (this *Conn) Writes(datas [][]byte, opts ...*Option) (err error) {
	return this.writes(flag_msg, datas, opts...)
}

// WARNING: 非线程安全
// 就是链接建立之前使用
func (this *Conn) Flush() error {
	return this.w.Flush()
}

// WARNING: 非线程安全
// 就是链接建立之前使用
// WARN: HandleMsg 处理接收到的消息。
// --------------
// ⚠️ 警告 (MEMORY UNSAFE):
// 传入的 data 切片直接引用连接内部的共享缓冲区 (readBuf)。
// 该数据仅在 HandleMsg 函数同步执行期间有效！
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
	return this.Sends(ctx, [][]byte{data}, opts...)
}

// WARNING: 线程安全
func (this *Conn) Sends(ctx context.Context, data [][]byte, opts ...*Option) (err error) {
	if this.closed.Load() {
		return fmt.Errorf("connection closed")
	}

	opt := this.opt.Merge(opts...)
	msg := msgPool.Get().(*msg)
	msg.flag = flag_msg
	msg.data = data
	if len(opts) > 0 {
		val := this.opt.Merge(opts...)
		msg.opt = &val // 只有这时才逃逸到堆
	} else {
		msg.opt = nil // 无额外选项，零分配
	}

	// 优化：直接使用 Timer，而不是包装 Context
	// 这样可以避免 Context 分配，且逻辑更清晰
	var timeoutCh <-chan time.Time
	if timeout := opt.SendChanTimeout; timeout != nil {
		timer := time.NewTimer(*timeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	select {
	case <-this.ctx.Done():
		msg.Release()
		return ErrConnectionClosed
	case <-ctx.Done():
		msg.Release()
		if ctx.Err() == context.DeadlineExceeded {
			// 这是业务层的总超时
			return fmt.Errorf("context deadline exceeded: %w", ctx.Err())
		}
		return fmt.Errorf("send cancelled: %w", ctx.Err())

	case <-timeoutCh:
		msg.Release()
		return ErrSendBufferFull
	case this.sendChan <- msg:
		return nil
	}
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

	if len(this.sendChan) > 0 {
		// 提取回调函数，避免在循环里多次判断
		callback := this.opt.OnCloseCallbackDiscardMsg
		var discardedMsgs [][][]byte

		// 循环抽干
		for {
			select {
			case msg := <-this.sendChan:
				if callback != nil {
					discardedMsgs = append(discardedMsgs, msg.data)
				}
				msg.Release() // 必须回收！
			default:
				// 通道空了，退出循环
				goto DRAINED
			}
		}

	DRAINED:
		if callback != nil && len(discardedMsgs) > 0 {
			callback(discardedMsgs)
		}
		this.opt.SetOnCloseCallbackDiscardMsg(nil)
	}

	this.l.Lock()
	err := this.closeErr
	this.l.Unlock()
	return err
}
