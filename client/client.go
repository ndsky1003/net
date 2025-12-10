package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ndsky1003/net/conn"
	"github.com/ndsky1003/net/logger"
)

type Client struct {
	name    string
	url     string
	opt     *Option
	conn    atomic.Pointer[conn.Conn]
	cancel  context.CancelFunc
	ctx     context.Context
	closeWg sync.WaitGroup
}

func Dial(ctx context.Context, name, url string, opts ...*Option) (c *Client, err error) {
	if name == "" {
		return nil, errors.New("client name is empty")
	}
	if url == "" {
		return nil, errors.New("client Dail url is empty")
	}
	opt := Options().SetReconnectInterval(2 * time.Second).Merge(opts...)
	ctx, cancel := context.WithCancel(ctx)
	c = &Client{
		name:   name,
		url:    url,
		opt:    &opt,
		ctx:    ctx,
		cancel: cancel,
	}
	c.closeWg.Add(1)
	go c.keepAlive()
	return
}

func (this *Client) keepAlive() {
	defer this.closeWg.Done()
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
	}
	for {
		select {
		case <-this.ctx.Done():
			return
		default:
		}
		conn_raw, err := dialer.DialContext(this.ctx, "tcp", this.url)
		if err != nil {
			logger.Errorf("keepAlive err:%v", err)
			// 如果是因为 context canceled 导致的错误，直接退出
			if errors.Is(err, context.Canceled) {
				return
			}
			select {
			case <-time.After(*this.opt.ReconnectInterval):
			case <-this.ctx.Done():
				return
			}
			continue
		}
		conn := conn.New(this.ctx, conn_raw, this.opt.GetHandler(), &this.opt.Option)

		err = this.serve(conn)
		if this.opt.OnDisconnected != nil {
			this.opt.OnDisconnected(err)
		}
		if err != nil {
			logger.Errorf("keepAlive server:%v", err)
		}
		delay := this.getReconnectDelay(err)
		select {
		case <-time.After(delay): //防止连上就断开，再继续连接
		case <-this.ctx.Done():
			return
		}
	}
}

func (this *Client) getReconnectDelay(err error) time.Duration {
	// 网络错误使用配置的重连间隔
	if isNetworkError(err) {
		return *this.opt.ReconnectInterval
	}

	// 其他错误使用默认间隔
	return *this.opt.ReconnectInterval
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

func (this *Client) Send(ctx context.Context, data []byte, opts ...*Option) error {
	conn := this.getConn()
	if conn == nil {
		return errors.New("connection not established")
	}
	opt := this.opt.Merge(opts...)
	return conn.Send(ctx, data, &opt.Option)
}

func (this *Client) Sends(ctx context.Context, data [][]byte, opts ...*Option) error {
	conn := this.getConn()
	if conn == nil {
		return errors.New("connection not established")
	}
	opt := this.opt.Merge(opts...)
	return conn.Sends(ctx, data, &opt.Option)
}

func (this *Client) serve(conn *conn.Conn) (err error) {
	if this.opt.OnConnected != nil {
		if err = this.opt.OnConnected(conn); err != nil {
			conn.Close()
			return
		}
	}
	this.setConn(conn)
	return conn.Serve()
}

func (this *Client) Close() error {
	this.cancel()
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

func (this *Client) GetUrl() string {
	return this.url
}

func (this *Client) GetOpt() *Option {
	return this.opt
}
