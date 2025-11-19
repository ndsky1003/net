package server

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/ndsky1003/net/conn"
)

type server struct {
	idCounter atomic.Int64
	mgr       IServiceManager
	opt       *Option
	ctx       context.Context
	cancel    context.CancelFunc
}

func NewServer(mgr IServiceManager, opts ...*Option) *server {
	ctx, cancel := context.WithCancel(context.Background())
	return &server{
		mgr:    mgr,
		opt:    Options().Merge(opts...),
		ctx:    ctx,
		cancel: cancel,
	}
}

func (this *server) Listen(addrs ...string) {
	for i := len(addrs) - 1; i >= 0; i-- {
		addr := addrs[i]
		if i != 0 {
			go this.listen(addr)
		} else {
			this.listen(addr)
		}
	}
}

func (this *server) listen(url string) error {
	lc := net.ListenConfig{}
	listener, err := lc.Listen(this.ctx, "tcp", url)
	if err != nil {
		return fmt.Errorf("listen err:%w", err)
	}
	defer listener.Close()
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
		go this.HandleConn(sid, conn)
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

func (this *server) HandleConn(sid string, conn *conn.Conn) (err error) {
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
