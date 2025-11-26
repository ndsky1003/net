package conn

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// WARNING: 非线程安全
func (this *Conn) Write(data []byte, opts ...*Option) (err error) {
	return this.write(flag_msg, data, opts...)
}

// WARNING: 非线程安全
func (this *Conn) Read(opts ...*Option) (data []byte, err error) {
	_, data, err = this.read(opts...)
	return
}

// WARNING: 线程安全
func (this *Conn) Send(data []byte, opts ...*Option) (err error) {
	if this.closed.Load() {
		return fmt.Errorf("connection closed")
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("connection closed:%w", err)
		}
	}()
	opt := Options().Merge(this.opt).Merge(opts...)
	msg := &msg{ //如果 Close() 先执行：sendChan 被关闭，select 会检测到通道关闭并panic
		flag: flag_msg,
		data: data,
		opt:  opt,
	}

	if timeout := opt.SendChanTimeout; timeout != nil {
		ctx, cancle := context.WithTimeout(context.Background(), *timeout)
		defer cancle()
		select {
		case this.sendChan <- msg:
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("send timeout")
			}
			return fmt.Errorf("send cancelled")
		case <-this.done:
			return fmt.Errorf("connection closed")
		}
	} else {
		select {
		case this.sendChan <- msg:
		case <-this.done:
			return fmt.Errorf("connection closed")
		}
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

	close(this.done)

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
	close(this.sendChan)

	this.l.Lock()
	err := this.closeErr
	this.l.Unlock()
	return err
}
