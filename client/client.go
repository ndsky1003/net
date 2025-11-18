package client

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ndsky1003/net/conn"
	"github.com/samber/lo"
)

type Handler interface {
	HandleMsg(data []byte) error
}
type HandlerFunc func([]byte) error

func (f HandlerFunc) HandleMsg(data []byte) error {
	return f(data)
}

// 定义认证常量
const (
	AuthSuccessByte = 0x0C
	AuthFailByte    = 0x00
)

var (
	AuthFailedError = errors.New("authentication failed")
)

type Client struct {
	version uuid.UUID
	name    string
	url     string
	opt     *Option
	conn    atomic.Pointer[conn.Conn]
}

func Dial(name, url string, opts ...*Option) (c *Client, err error) {
	if name == "" {
		return nil, errors.New("client name is empty")
	}
	if url == "" {
		return nil, errors.New("client Dail url is empty")
	}
	c = &Client{
		version: lo.Must(uuid.NewV7()),
		name:    name,
		url:     url,
		opt:     Options().SetReconnectInterval(2 * time.Second).Merge(opts...),
	}
	go c.keepAlive()
	return
}

func (this *Client) keepAlive() {
	for {
		conn_raw, err := net.Dial("tcp", this.url)
		if err != nil {
			log.Println("err:", err)
			time.Sleep(*this.opt.ReconnectInterval)
			continue
		}
		conn := conn.New(conn_raw, this, &this.opt.Option)

		err = this.serve(conn)
		if err != nil {
			log.Println("server:", err)
		}
		delay := this.getReconnectDelay(err)
		time.Sleep(delay) //防止连上就断开，再继续连接
	}
}

func (this *Client) getReconnectDelay(err error) time.Duration {
	// 认证失败应该使用更长的延迟，避免快速重试
	if isAuthError(err) {
		return 5 * time.Second // 认证失败等待更久
	}

	// 网络错误使用配置的重连间隔
	if isNetworkError(err) {
		return *this.opt.ReconnectInterval
	}

	// 其他错误使用默认间隔
	return *this.opt.ReconnectInterval
}

// 错误类型判断函数
func isAuthError(err error) bool {
	return err != nil &&
		(errors.Is(err, AuthFailedError) || err.Error() == "authentication failed" ||
			err.Error() == "verification failed")
}

func isNetworkError(err error) bool {
	// 根据实际错误类型判断
	return err != nil &&
		(err.Error() == "connection refused" ||
			err.Error() == "network is unreachable")
}

func (this *Client) setConn(newConn *conn.Conn) {
	oldConn := this.getConn()
	if oldConn != nil {
		oldConn.Close() // 关闭旧连接
	}
	this.conn.Store(newConn)
}

func (this *Client) getConn() *conn.Conn {
	return this.conn.Load()
}

func (this *Client) HandleMsg(data []byte) error {
	return this.opt.Handler.HandleMsg(data)
}

func (this *Client) Send(data []byte, opts ...*Option) error {
	conn := this.getConn()
	if conn == nil {
		return errors.New("connection not established")
	}
	opt := Options().Merge(this.opt).Merge(opts...)
	return conn.Send(data, &opt.Option)
}

func (this *Client) verify(conn *conn.Conn) (err error) {
	if this.opt.Secret == nil || *this.opt.Secret == "" {
		return
	}
	if err = conn.Write([]byte(*this.opt.Secret)); err != nil {
		return
	}

	res, err := conn.Read()
	if err != nil {
		return fmt.Errorf("read auth response failed: %w", err)
	}

	if len(res) == 0 || res[0] != AuthSuccessByte {
		return AuthFailedError
	}

	return nil
}

func (this *Client) serve(conn *conn.Conn) (err error) {
	if err = this.verify(conn); err != nil {
		conn.Close()
		return
	}
	this.setConn(conn)
	return conn.Serve()
}

func (this *Client) Close() error {
	conn := this.conn.Load()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (this *Client) IsConnected() bool {
	return this.conn.Load() != nil
}

func (this *Client) GetName() string {
	return this.name
}

func (this *Client) GetVersion() uuid.UUID {
	return this.version
}

func (this *Client) GetUrl() string {
	return this.url
}

func (this *Client) GetOpt() *Option {
	return this.opt
}
