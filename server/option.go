package server

import (
	"time"

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

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
	}

	if delta.Secret != nil {
		this.Secret = delta.Secret
	}

	if delta.VerifyTimeout != nil {
		this.VerifyTimeout = delta.VerifyTimeout
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

// NOTE: 重写的目的,是方便返回的是当前的Option,而不是conn.Option
// ---------------------重写conn.Option的设置函数---------------------
func (this *Option) SetReadTimeout(t time.Duration) *Option {
	this.ReadTimeout = &t
	return this
}

func (this *Option) SetReadTimeoutFactor(t float64) *Option {
	this.ReadTimeoutFactor = &t
	return this
}

func (this *Option) SetWriteTimeout(t time.Duration) *Option {
	this.WriteTimeout = &t
	return this
}

func (this *Option) SetSendChanTimeout(t time.Duration) *Option {
	this.SendChanTimeout = &t
	return this
}

func (this *Option) SetTimeout(t time.Duration) *Option {
	this.SetReadTimeout(t).SetWriteTimeout(t)
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
