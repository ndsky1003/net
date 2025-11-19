package conn

import (
	"fmt"
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

// NOTE: 线程安全
func (this *Conn) Send(data []byte, opts ...*Option) (err error) {
	if this.closed.Load() {
		return fmt.Errorf("connection closed")
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("connection closed")
		}
	}()
	select {
	case this.sendChan <- &msg{ //如果 Close() 先执行：sendChan 被关闭，select 会检测到通道关闭并panic
		flag: flag_msg,
		data: data,
		opt:  Options().Merge(this.opt).Merge(opts...),
	}:
	default:
		return fmt.Errorf("send buffer full")
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
	if err := this.Conn.Close(); err != nil {
		return err
	}
	close(this.sendChan)
	// this.done = nil
	// this.sendChan = nil // 设置为nil有可能造成panic，读写都会
	return nil
}
