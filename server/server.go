package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ndsky1003/net/conn"
	"github.com/ndsky1003/net/logger"
)

type server struct {
	idCounter atomic.Int64
	mgr       service_manager
	opt       *Option
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	once      sync.Once
}

func New(ctx context.Context, mgr service_manager, opts ...*Option) *server {
	ctx, cancel := context.WithCancel(ctx)
	opt := Options().SetVerifyTimeout(5 * time.Second).Merge(opts...)
	return &server{
		mgr:    mgr,
		opt:    &opt,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (this *server) Listen(addrs ...string) (err error) {
	defer this.Close()
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
			// 1. 优先检查 Context 是否取消（服务器关闭信号）
			if this.ctx.Err() != nil {
				return nil
			}

			// 2. 【现代写法】检查是否是连接已关闭错误 (net.ErrClosed)
			// Go 1.16+ 引入了 net.ErrClosed，这是最准确的判断方式
			if errors.Is(err, net.ErrClosed) {
				return nil // 正常退出
			}

			// 3. 对于所有其他错误（超时、EMFILE、网络抖动等），都视为“临时错误”进行退避重试
			// 这样既避免了使用弃用的 Temporary()，也能防止因文件句柄耗尽导致服务器崩溃
			if tempDelay == 0 {
				tempDelay = 5 * time.Millisecond
			} else {
				tempDelay *= 2
			}
			if tempDelay > 1*time.Second {
				tempDelay = 1 * time.Second
			}

			logger.Infof("Accept error: %v; retrying in %v", err, tempDelay)
			time.Sleep(tempDelay)
			continue
		}
		// 成功建立连接，重置延迟
		tempDelay = 0
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
				logger.Infof("failed to notify client of auth failure, sid: %s, err: %v", sid, writeErr)
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

func (this *server) Close() (err error) {
	this.once.Do(func() {
		this.cancel()
		this.wg.Wait()
		err = this.mgr.Close()
	})
	return
}
