package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ndsky1003/net/conn"
)

type server struct {
	idCounter atomic.Int64
	mgr       service_manager
	opt       *Option
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func New(ctx context.Context, mgr service_manager, opts ...*Option) *server {
	ctx, cancel := context.WithCancel(ctx)
	return &server{
		mgr:    mgr,
		opt:    Options().SetVerifyTimeout(5 * time.Second).Merge(opts...),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (this *server) Listen(addrs ...string) (err error) {
	length := len(addrs)
	listeners := make([]net.Listener, 0, length)
	errCh := make(chan error, length)
	for i, addr := range addrs {
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
	var tempDelay time.Duration // 临时错误重试延迟
	for {
		connRaw, err := listener.Accept()
		if err != nil {
			if this.ctx.Err() != nil {
				// 上下文被取消，正常退出
				return nil
			}
			// 检查是否为临时错误
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if tempDelay > 1*time.Second {
					tempDelay = 1 * time.Second
				}
				log.Printf("Accept error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			tempDelay = 0 // 重置延迟
			return fmt.Errorf("accept err:%w", err)
		}
		sid := this.genSessionID(this.idCounter.Add(1))
		helper := &handler_helper{
			sid:    sid,
			server: this,
		}
		conn := conn.New(this.ctx, connRaw, helper, &this.opt.Option)
		this.wg.Add(1)
		go this.handleConn(sid, conn)
	}
}

func (this *server) genSessionID(index int64) string {
	return fmt.Sprintf("%s-%d", uuid.New().String(), index)
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

func (this *server) handleConn(sid string, c *conn.Conn) (err error) {
	defer this.wg.Done()
	if this.opt.Secret != nil && *this.opt.Secret != "" {
		opt := conn.Options()
		if this.opt.VerifyTimeout != nil {
			opt.SetReadTimeout(*this.opt.VerifyTimeout)
		}
		if res, err := c.Read(opt); err != nil {
			return fmt.Errorf("read auth failed: %w", err)
		} else if string(res) != *this.opt.Secret {
			if writeErr := c.Write([]byte{auth_fail_byte}); writeErr != nil {
				log.Printf("failed to notify client of auth failure, sid: %s, err: %v", sid, writeErr)
			}
			return errors.New("authentication failed")
		}
		if err = c.Write([]byte{auth_success_byte}); err != nil {
			return
		}
	}
	err = this.mgr.OnConnect(sid, c)
	if err != nil {
		c.Close()
		return
	}
	err = c.Serve()
	disconnectErr := this.mgr.OnDisconnect(sid, err)
	if err == nil {
		err = disconnectErr
	}
	return
}

func (this *server) Close() error {
	this.cancel()
	this.wg.Wait()
	return nil
}
