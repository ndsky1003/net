// NOTE: 如果这里面加了设置函数，都需要再内嵌的地方加一遍，因为返回值要返回当前的Option，才会形成链式调用
package conn

import (
	"time"

	"github.com/ndsky1003/net/comm/ut"
)

func Options() *Option {
	return &Option{}
}

type Option struct {
	ReadTimeout               *time.Duration
	ReadTimeoutFactor         *float64 // 读超时倍数因子，默认2.2
	WriteTimeout              *time.Duration
	SendChanTimeout           *time.Duration //不设置满了就会丢掉
	HeartInterval             *time.Duration
	SendChanSize              *int
	OnCloseCallbackDiscardMsg func(data [][][]byte) //分线的数据包,并没有再次合起来{header,meta,body}
	ReadBufferLimitSize       *uint64               // 最大支持读取缓冲区大小,防止内存被撑爆 default 100M
	ReadBufferMinSize         *int                  // 读取缓冲区大小,最小值,用于动态扩容 default 4k
	ReadBufferMaxSize         *int                  // 读取缓冲区大小,最大值,用于动态扩容 default 64k
	ShrinkThreshold           *int                  // 读取缓冲区缩容阈值 ,default 50
}

func (this *Option) SetReadTimeout(t time.Duration) *Option {
	this.ReadTimeout = &t
	return this
}

func (this *Option) SetReadBufferLimitSize(delta uint64) *Option {
	this.ReadBufferLimitSize = &delta
	return this
}

func (this *Option) SetReadBufferMinSize(size int) *Option {
	this.ReadBufferMinSize = &size
	return this
}

func (this *Option) SetReadBufferMaxSize(size int) *Option {
	this.ReadBufferMaxSize = &size
	return this
}

func (this *Option) SetShrinkThreshold(t int) *Option {
	this.ShrinkThreshold = &t
	return this
}

func (this *Option) SetReadTimeoutFactor(t float64) *Option {
	this.ReadTimeoutFactor = &t
	return this
}

func (this *Option) SetTimeout(t time.Duration) *Option {
	return this.SetReadTimeout(t).SetWriteTimeout(t)
}

func (this *Option) SetWriteTimeout(t time.Duration) *Option {
	this.WriteTimeout = &t
	return this
}

func (this *Option) SetSendChanTimeout(t time.Duration) *Option {
	this.SendChanTimeout = &t
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

func (this *Option) SetOnCloseCallbackDiscardMsg(f func(data [][][]byte)) *Option {
	this.OnCloseCallbackDiscardMsg = f
	return this
}

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
	}
	ut.ResolveOption(&this.ReadTimeout, delta.ReadTimeout)
	ut.ResolveOption(&this.WriteTimeout, delta.WriteTimeout)
	ut.ResolveOption(&this.SendChanTimeout, delta.SendChanTimeout)
	ut.ResolveOption(&this.HeartInterval, delta.HeartInterval)
	ut.ResolveOption(&this.SendChanSize, delta.SendChanSize)
	ut.ResolveOption(&this.ReadTimeoutFactor, delta.ReadTimeoutFactor)
	ut.ResolveOption(&this.ReadBufferLimitSize, delta.ReadBufferLimitSize)
	ut.ResolveOption(&this.ReadBufferMinSize, delta.ReadBufferMinSize)
	ut.ResolveOption(&this.ReadBufferMaxSize, delta.ReadBufferMaxSize)
	ut.ResolveOption(&this.ShrinkThreshold, delta.ShrinkThreshold)

	if delta.OnCloseCallbackDiscardMsg != nil {
		this.OnCloseCallbackDiscardMsg = delta.OnCloseCallbackDiscardMsg
	}
	return this
}

func (this Option) Merge(opts ...*Option) Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}
