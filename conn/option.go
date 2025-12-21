// NOTE: 如果这里面加了设置函数，都需要再内嵌的地方加一遍，因为返回值要返回当前的Option，才会形成链式调用
package conn

import (
	"time"

	"github.com/ndsky1003/net/v2/comm/ut"
)

func Options() *Option {
	return &Option{}
}

type Option struct {
	ReadTimeout               *time.Duration //仅限于握手阶段的超时
	ReadTimeoutFactor         *float64       //超时的因子，由后面的公式计算出超时时间time.Duration(float64(heartInterval) * *this.opt.ReadTimeoutFactor)
	WriteTimeout              *time.Duration
	SendChanTimeout           *time.Duration //当设置值小于或者等于0的时候会在满buff的时候自动丢弃
	HeartInterval             *time.Duration
	SendChanSize              *int
	OnCloseCallbackDiscardMsg func(data [][][]byte) //分线的数据包,并没有再次合起来{header,meta,body}
	ReadBufferLimitSize       *uint64               // 最大支持读取缓冲区大小,防止内存被撑爆 default 100M
	GenBufFn                  func() []byte         // 生成读取缓冲区函数
	SendDeferFn               func()                // 发送后置函数，不管成功与否都会执行,应用场景，就是发送的[]byte,是池化的，发送又是异步，需要在发送完成或者失败，上报给上层
}

func (this *Option) SetReadTimeout(t time.Duration) *Option {
	this.ReadTimeout = &t
	return this
}

func (this *Option) SetReadBufferLimitSize(delta uint64) *Option {
	this.ReadBufferLimitSize = &delta
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

func (this *Option) SetGenBufFn(f func() []byte) *Option {
	this.GenBufFn = f
	return this
}

func (this *Option) SetSendDeferFn(f func()) *Option {
	this.SendDeferFn = f
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

	if delta.OnCloseCallbackDiscardMsg != nil {
		this.OnCloseCallbackDiscardMsg = delta.OnCloseCallbackDiscardMsg
	}

	if delta.GenBufFn != nil {
		this.GenBufFn = delta.GenBufFn
	}

	if delta.SendDeferFn != nil {
		this.SendDeferFn = delta.SendDeferFn
	}

	return this
}

func (this Option) Merge(opts ...*Option) Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}
