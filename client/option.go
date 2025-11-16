package client

import "time"

type Option struct {
	ReconnectInterval *time.Duration
	Secret            *string
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

	return this
}

func (this *Option) Merge(opts ...*Option) *Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}
