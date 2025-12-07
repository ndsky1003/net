package client

import (
	"time"

	"github.com/ndsky1003/net/comm/ut"
	"github.com/ndsky1003/net/conn"
)

type Option struct {
	Secret            *string
	VerifyTimeout     *time.Duration
	ReconnectInterval *time.Duration
	conn.Handler
	conn.Option
	OnConnected    func(*conn.Conn) error // 这里有可能校验逻辑,返回所错误要跳出重连
	OnDisconnected func(err error)
	OnAuthFailed   func(err error)
}

func Options() *Option {
	return &Option{}
}

func (this *Option) WithConn(fn func(*conn.Option)) *Option {
	if fn != nil {
		fn(&this.Option)
	}
	return this
}

// WARN: 这个函数在conn.Serve之前,所以只能使用conn.Write\Read\Flush等基础方法
func (this *Option) SetOnConnected(f func(*conn.Conn) error) *Option {
	this.OnConnected = f
	return this
}

func (this *Option) SetVerifyTimeout(t time.Duration) *Option {
	this.VerifyTimeout = &t
	return this
}

func (this *Option) SetOnDisconnected(f func(err error)) *Option {
	this.OnDisconnected = f
	return this
}

func (this *Option) SetOnAuthFailed(f func(err error)) *Option {
	this.OnAuthFailed = f
	return this
}

func (this *Option) SetReconnectInterval(t time.Duration) *Option {
	this.ReconnectInterval = &t
	return this
}

func (this *Option) SetSecret(s string) *Option {
	this.Secret = &s
	return this
}

// SetHandler的一个包装，类似http的HandlerFunc
func (this *Option) SetHandlerFunc(f func([]byte) error) *Option {
	this.Handler = handler_func(f)
	return this
}

func (this *Option) SetHandler(f conn.Handler) *Option {
	this.Handler = f
	return this
}

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
	}

	ut.ResolveOption(&this.ReconnectInterval, delta.ReconnectInterval)
	ut.ResolveOption(&this.VerifyTimeout, delta.VerifyTimeout)
	ut.ResolveOption(&this.Secret, delta.Secret)

	if delta.Handler != nil {
		this.Handler = delta.Handler
	}

	if delta.OnConnected != nil {
		this.OnConnected = delta.OnConnected
	}

	if delta.OnDisconnected != nil {
		this.OnDisconnected = delta.OnDisconnected
	}

	if delta.OnAuthFailed != nil {
		this.OnAuthFailed = delta.OnAuthFailed
	}

	this.Option = this.Option.Merge(&delta.Option)
	return this
}

func (this Option) Merge(opts ...*Option) Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}

func (this *Option) GetHandler() conn.Handler {
	if this.Handler != nil {
		return this.Handler
	}
	return &default_handler{}
}

type default_handler struct {
}

func (this *default_handler) HandleMsg(data []byte) error {
	return nil
}

type handler_func func([]byte) error

func (f handler_func) HandleMsg(data []byte) error {
	return f(data)
}
