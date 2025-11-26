package client

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ndsky1003/net/conn"
	"github.com/samber/lo"
)

// 定义认证常量
const (
	auth_success_byte = 0x0C
)

var (
	auth_failed_error = errors.New("authentication failed")
)

type Client struct {
	version uuid.UUID
	name    string
	url     string
	opt     *Option
	conn    atomic.Pointer[conn.Conn]
	closeCh chan struct{}
	closeWg sync.WaitGroup
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
		closeCh: make(chan struct{}),
	}
	c.closeWg.Add(1)
	go c.keepAlive()
	return
}

func (this *Client) keepAlive() {
	defer this.closeWg.Done()
	for {
		select {
		case <-this.closeCh:
			return
		default:
		}
		conn_raw, err := net.DialTimeout("tcp", this.url, 5*time.Second)
		if err != nil {
			log.Println("err:", err)
			select {
			case <-time.After(*this.opt.ReconnectInterval):
			case <-this.closeCh:
				return
			}
			continue
		}
		conn := conn.New(conn_raw, this.opt.GetHandler(), &this.opt.Option)

		err = this.serve(conn)
		if this.opt.OnDisconnected != nil {
			this.opt.OnDisconnected(err)
		}
		if err != nil {
			log.Println("server:", err)
		}
		delay := this.getReconnectDelay(err)
		select {
		case <-time.After(delay): //防止连上就断开，再继续连接
		case <-this.closeCh:
			return
		}
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
		(errors.Is(err, auth_failed_error) || err.Error() == "authentication failed" ||
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

func (this *Client) Send(data []byte, opts ...*Option) error {
	conn := this.getConn()
	if conn == nil {
		return errors.New("connection not established")
	}
	opt := Options().Merge(this.opt).Merge(opts...)
	return conn.Send(data, &opt.Option)
}

func (this *Client) verify(c *conn.Conn) (err error) {
	if this.opt.Secret == nil || *this.opt.Secret == "" {
		return
	}
	if err = c.Write([]byte(*this.opt.Secret)); err != nil {
		return
	}

	res, err := c.Read(conn.Options().SetReadDeadline(5 * time.Second))
	if err != nil {
		return fmt.Errorf("read auth response failed: %w", err)
	}

	if len(res) == 0 || res[0] != auth_success_byte {
		return auth_failed_error
	}

	return nil
}

func (this *Client) serve(conn *conn.Conn) (err error) {
	if err = this.verify(conn); err != nil {
		if this.opt.OnAuthFailed != nil {
			this.opt.OnAuthFailed(err)
		}
		conn.Close()
		return
	}
	this.setConn(conn)
	if this.opt.OnConnected != nil {
		this.opt.OnConnected()
	}
	return conn.Serve()
}

func (this *Client) Close() error {
	select {
	case <-this.closeCh:
		// already closed
		return nil
	default:
		close(this.closeCh)
	}

	conn := this.conn.Load()
	if conn != nil {
		conn.Close()
	}
	this.closeWg.Wait()
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
