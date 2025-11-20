package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/ndsky1003/net/conn"
)

type server struct {
	idCounter atomic.Int64
	mgr       service_manager
	opt       *Option
	ctx       context.Context
	cancel    context.CancelFunc
}

func New(mgr service_manager, opts ...*Option) *server {
	ctx, cancel := context.WithCancel(context.Background())
	return &server{
		mgr:    mgr,
		opt:    Options().Merge(opts...),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (this *server) Listen(addrs ...string) (err error) {
	length := len(addrs)
	listeners := make([]net.Listener, 0, length)
	errCh := make(chan error, length)
	for i := 0; i < length; i++ {
		addr := addrs[i]
		listener, err := this.listen(addr)
		if err != nil {
			// 清理已创建的监听器
			for j := 0; j < i; j++ {
				listeners[j].Close()
			}
			return err
		}
		listeners = append(listeners, listener)
	}
	for _, listener := range listeners {
		go func(l net.Listener) {
			errCh <- this.acceptListener(l)
		}(listener)
	}

	select {
	case err := <-errCh:
		for _, listener := range listeners {
			listener.Close()
		}
		return err
	case <-this.ctx.Done():
		for _, listener := range listeners {
			listener.Close()
		}
		return nil
	}
}

func (this *server) listen(url string) (net.Listener, error) {
	lc := net.ListenConfig{}
	listener, err := lc.Listen(this.ctx, "tcp", url)
	if err != nil {
		return nil, fmt.Errorf("listen err:%w", err)
	}
	return listener, nil
}

func (this *server) acceptListener(listener net.Listener) error {
	for {
		connRaw, err := listener.Accept()
		if err != nil {
			if this.ctx.Err() != nil {
				// 上下文被取消，正常退出
				return nil
			}
			return fmt.Errorf("accept err:%w", err)
		}
		sid := this.genSessionID(this.idCounter.Add(1))
		helper := &handler_helper{
			sid:    sid,
			server: this,
		}
		conn := conn.New(connRaw, helper, &this.opt.Option)
		go this.handleConn(sid, conn)
	}
}

func (this *server) genSessionID(index int64) string {
	return fmt.Sprintf("service_%d", index)
}

type handler_helper struct {
	sid string
	*server
}

func (this *handler_helper) HandleMsg(data []byte) error {
	return this.mgr.OnMessage(this.sid, data)
}

// 定义认证常量
const (
	auth_success_byte = 0x0C
	auth_fail_byte    = 0x00
)

func (this *server) handleConn(sid string, conn *conn.Conn) (err error) {
	if this.opt.Secret != nil && *this.opt.Secret != "" {
		if data, err := conn.Read(); err != nil {
			return err
		} else if string(data) != *this.opt.Secret {
			if err := conn.Write([]byte{auth_fail_byte}); err != nil {
				return err
			}
			return errors.New("authentication failed")
		}
		if err = conn.Write([]byte{auth_success_byte}); err != nil {
			return
		}
	}
	err = this.mgr.OnConnect(sid, conn)
	if err != nil {
		conn.Close()
		return
	}
	err = conn.Serve()
	disconnectErr := this.mgr.OnDisconnect(sid, err)
	if err == nil {
		err = disconnectErr
	}
	return
}

func (this *server) Close() error {
	this.cancel()
	return this.mgr.Close()
}
