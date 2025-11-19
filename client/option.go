package client

import (
	"time"

	"github.com/ndsky1003/net/conn"
)

type Option struct {
	Secret            *string
	ReconnectInterval *time.Duration
	Handler
	conn.Option
}

func Options() *Option {
	return &Option{}
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

func (this *Option) SetHandler(f Handler) *Option {
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

	this.Option.Merge(&delta.Option)

	return this
}

func (this *Option) Merge(opts ...*Option) *Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}
