package server

import (
	"fmt"
	"net"

	"github.com/ndsky1003/net/conn"
)

type server struct {
	mgr ServiceManager
	opt *Option
}

func NewServer(mgr ServiceManager, opts ...*Option) *server {
	return &server{
		opt: Options().Merge(opts...),
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
	listen, err := net.Listen("tcp", url)
	if err != nil {
		return fmt.Errorf("listen err:%w", err)
	}
	for {
		conn_raw, err := listen.Accept()
		if err != nil {
			return fmt.Errorf("accept err:%w", err)
		}
		conn := conn.New(conn_raw, this, &this.opt.Option)
		go this.HandleConn(conn)
	}
}

func (this *server) HandleMsg(data []byte) error {
	this.mgr.OnMessage("", data)
	return nil
}

func (this *server) HandleConn(conn *conn.Conn) {
	this.mgr.OnConnect(conn, "")
}

func (this *server) Close() {

}
