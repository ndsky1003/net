package client

import (
	"time"

	"github.com/ndsky1003/net/conn"
)

type Option struct {
	Secret            *string
	ReconnectInterval *time.Duration
	conn.Handler
	conn.Option
	OnConnected    func()
	OnDisconnected func(err error)
	OnAuthFailed   func(err error)
}

func Options() *Option {
	return &Option{}
}

func (this *Option) SetOnConnected(f func()) *Option {
	this.OnConnected = f
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

	if delta.ReconnectInterval != nil {
		this.ReconnectInterval = delta.ReconnectInterval
	}

	if delta.Secret != nil {
		this.Secret = delta.Secret
	}

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

	this.Option.Merge(&delta.Option)

	return this
}

func (this *Option) Merge(opts ...*Option) *Option {
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

// ---------------------重写conn.Option的设置函数---------------------
func (this *Option) SetReadDeadline(t time.Duration) *Option {
	this.ReadDeadline = &t
	return this
}

func (this *Option) SetWriteDeadline(t time.Duration) *Option {
	this.WriteDeadline = &t
	return this
}

func (this *Option) SetSendChanTimeout(t time.Duration) *Option {
	this.SendChanTimeout = &t
	return this
}

func (this *Option) SetDeadline(t time.Duration) *Option {
	this.SetReadDeadline(t).SetWriteDeadline(t)
	return this
}

func (this *Option) SetHeartInterval(t time.Duration) *Option {
	this.HeartInterval = &t
	return this
}

func (this *Option) SetSendChanSize(t int) *Option {
	this.SendChanSize = &t
	return this
}

func (this *Option) SetMaxFrameSize(delta uint64) *Option {
	this.MaxFrameSize = &delta
	return this
}

func (this *Option) SetOnCloseCallbackDiscardMsg(f func(data [][]byte)) *Option {
	this.OnCloseCallbackDiscardMsg = f
	return this
}

//---------------------重写conn.Option的设置函数---------------------
