package server

import (
	"time"

	"github.com/ndsky1003/net/comm/ut"
	"github.com/ndsky1003/net/conn"
)

type Option struct {
	Secret        *string
	VerifyTimeout *time.Duration
	conn.Option
}

func Options() *Option {
	return &Option{}
}

func (this *Option) SetSecret(s string) *Option {
	this.Secret = &s
	return this
}

func (this *Option) SetVerifyTimeout(t time.Duration) *Option {
	this.VerifyTimeout = &t
	return this
}

func (this *Option) WithConn(fn func(*conn.Option)) *Option {
	if fn != nil {
		fn(&this.Option)
	}
	return this
}

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
	}

	ut.ResolveOption(&this.Secret, delta.Secret)
	ut.ResolveOption(&this.VerifyTimeout, delta.VerifyTimeout)

	this.Option = this.Option.Merge(&delta.Option)
	return this
}

func (this Option) Merge(opts ...*Option) Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}
