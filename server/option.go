package server

import (
	"github.com/ndsky1003/net/conn"
)

type Option struct {
	conn.Option
}

func Options() *Option {
	return &Option{}
}

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
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
