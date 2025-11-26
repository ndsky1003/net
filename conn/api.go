package conn

import (
	"context"
	"fmt"
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
	defer this.Close()
	errCh := make(chan error, 2)

	go func() {
		errCh <- this.writePump()
	}()

	go func() {
		errCh <- this.readPump()
	}()

	// 返回第一个错误
	return <-errCh
}

func (this *Conn) Close() error {
	if !this.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(this.done)
	close(this.sendChan)
	if f := this.opt.OnCloseCallbackDiscardMsg; f != nil {
		var discardedMsgs [][]byte
		for msg := range this.sendChan {
			discardedMsgs = append(discardedMsgs, msg.data)
		}
		f(discardedMsgs)
		this.opt.SetOnCloseCallbackDiscardMsg(nil) //把回调释放掉，其他conn还会引用的
	}

	this.Conn.SetReadDeadline(time.Now()) // 立即解除阻塞的Read操作 ，这是一个防御性编程，下面的 Close 也可以关闭
	if err := this.Conn.Close(); err != nil {
		return err
	}
	// this.done = nil
	// this.sendChan = nil // 设置为nil有可能造成panic，读写都会
	return nil
}
