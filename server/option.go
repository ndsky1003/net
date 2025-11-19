package server

import (
	"github.com/ndsky1003/net/conn"
)

type Option struct {
	Secret *string
	conn.Option
}

func Options() *Option {
	return &Option{}
}

func (this *Option) SetSecret(s string) *Option {
	this.Secret = &s
	return this
}

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
	}

	if delta.Secret != nil {
		this.Secret = delta.Secret
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
