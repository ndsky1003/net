package server

import (
	"time"

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
