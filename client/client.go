package client

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
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
	opt := Options().
		SetBaseReconnectInterval(2 * time.Second).
		SetMaxReconnectInterval(60 * time.Second).
		Merge(opts...)
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

	var attempts int

	for {
		select {
		case <-this.ctx.Done():
			return
		default:
		}

		conn_raw, err := dialer.DialContext(this.ctx, "tcp", this.url)
		if err != nil {
			attempts++

			// 如果是因为 context canceled 导致的错误，直接退出
			if errors.Is(err, context.Canceled) {
				return
			}

			delay := this.getReconnectDelay(err, attempts)
			logger.Errorf("keepAlive dial err: %v, retry in %v (attempt %d)", err, delay, attempts)

			select {
			case <-time.After(delay):
			case <-this.ctx.Done():
				return
			}
			continue
		}

		//NOTE:如果想防止“连上立刻断”的抖动，可以在 serve 正常运行一段时间后再重置
		attempts = 0

		conn := conn.New(this.ctx, conn_raw, this.opt.GetHandler(), &this.opt.Option)

		// 阻塞运行直到断开...
		err = this.serve(conn)

		if this.opt.OnDisconnected != nil {
			this.opt.OnDisconnected(err)
		}
		if err != nil {
			logger.Errorf("keepAlive server disconnected: %v", err)
		}

		// 服务断开，这也算是一种需要“重连”的状态
		// 我们可以把 attempts 设为 1，避免立即无间隔重连
		attempts++

		delay := this.getReconnectDelay(err, attempts)
		select {
		case <-time.After(delay):
		case <-this.ctx.Done():
			return
		}
	}
}

func (this *Client) getReconnectDelay(_ error, attempts int) time.Duration {
	base := *this.opt.BaseReconnectInterval
	maxDelay := *this.opt.MaxReconnectInterval
	if base <= 0 {
		base = 2 * time.Second
	}
	if attempts > 20 {
		attempts = 20
	}
	backoff := float64(base) * math.Pow(2, float64(attempts-1))

	// 5. 截断到上限
	if backoff > float64(maxDelay) {
		backoff = float64(maxDelay)
	}

	if backoff > float64(base) {
		randomFactor := rand.Float64()
		backoff = float64(base) + randomFactor*(backoff-float64(base))
	}
	return time.Duration(backoff)
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
