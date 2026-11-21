package net

import (
	"time"
)

func resolveOption[T any](old **T, new *T) {
	if new != nil {
		*old = new
	}
}

// 运行时参数默认值，均可通过 Option 覆盖。
const (
	defaultMaxFrameSize   = 64 << 10 //64KB
	defaultMaxBatchFrames = 512      // IOV_MAX=1024，每帧最多 2 个 iovec，一批最多 512 帧
	defaultReadBlockSize  = 64 << 10 // 接收端把大 body 分块读入的粒度
	defaultSendChanSize   = 30
	defaultMsgChanSize    = defaultMaxBatchFrames // 最低得是 maxBatchFrames
)

// Option 是 Conn 的可配置参数，用 Options() 构造
type Option struct {
	HeartInterval      *time.Duration // 看门狗周期；空闲超过它才发 PING
	ReadTimeoutFactor  *float64       // 判死时限 = HeartInterval * 该系数，默认 2.2
	WriteTimeoutFactor *float64       // 单次 writev 的上限 = HeartInterval * 该系数，默认 2.2
	MaxFrameSize       *int           // 单帧 body 上限，默认 64KB
	MaxBatchFrames     *int           // 一次 writev 最多攒多少帧，默认 512
	ReadBlockSize      *int           // 接收端把大 body 分块读入的粒度，默认 64KB
	SendChanSize       *int           // sendChan 缓冲，默认 30
	MsgChanSize        *int           // msgChan 缓冲，默认 = MaxBatchFrames（最低得是这个值）
}

func Options() *Option {
	return new(Option)
}

func (this *Option) SetHeartInterval(d time.Duration) *Option {
	if this == nil {
		return this
	}
	this.HeartInterval = &d
	return this
}

func (this *Option) SetReadTimeoutFactor(f float64) *Option {
	if this == nil {
		return this
	}
	this.ReadTimeoutFactor = &f
	return this
}

func (this *Option) SetWriteTimeoutFactor(f float64) *Option {
	if this == nil {
		return this
	}
	this.WriteTimeoutFactor = &f
	return this
}

func (this *Option) SetMaxFrameSize(n int) *Option {
	if this == nil {
		return this
	}
	this.MaxFrameSize = &n
	return this
}

func (this *Option) SetMaxBatchFrames(n int) *Option {
	if this == nil {
		return this
	}
	this.MaxBatchFrames = &n
	return this
}

func (this *Option) SetReadBlockSize(n int) *Option {
	if this == nil {
		return this
	}
	this.ReadBlockSize = &n
	return this
}

func (this *Option) SetSendChanSize(n int) *Option {
	if this == nil {
		return this
	}
	this.SendChanSize = &n
	return this
}

func (this *Option) SetMsgChanSize(n int) *Option {
	if this == nil {
		return this
	}
	this.MsgChanSize = &n
	return this
}

func (this *Option) merges(opts ...*Option) *Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}

func (this *Option) merge(opt *Option) {
	if opt == nil {
		return
	}
	resolveOption(&this.HeartInterval, opt.HeartInterval)
	resolveOption(&this.ReadTimeoutFactor, opt.ReadTimeoutFactor)
	resolveOption(&this.WriteTimeoutFactor, opt.WriteTimeoutFactor)
	resolveOption(&this.MaxFrameSize, opt.MaxFrameSize)
	resolveOption(&this.MaxBatchFrames, opt.MaxBatchFrames)
	resolveOption(&this.ReadBlockSize, opt.ReadBlockSize)
	resolveOption(&this.SendChanSize, opt.SendChanSize)
	resolveOption(&this.MsgChanSize, opt.MsgChanSize)
}
