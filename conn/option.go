package conn

import "time"

type Option struct {
	ReadDeadline  *time.Duration
	WriteDeadline *time.Duration
	HeartInterval *time.Duration
	SendChanSize  *int
	// 适合大多数消息传递场景
	//opt := Options().SetMaxFrameSize(64 * 1024)  // 64KB
	// 适合大文件分块传输
	//opt := Options().SetMaxFrameSize(10 * 1024 * 1024)  // 10MB
	// 只有在特殊场景下才考虑 100M
	//opt := Options().SetMaxFrameSize(100 * 1024 * 1024)  // 绝对上限！
	MaxFrameSize *uint64
}

func Options() *Option {
	return &Option{}
}

func (this *Option) SetReadDeadline(t time.Duration) *Option {
	this.ReadDeadline = &t
	return this
}

func (this *Option) SetWriteDeadline(t time.Duration) *Option {
	this.WriteDeadline = &t
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

func (this *Option) merge(delta *Option) *Option {
	if this == nil || delta == nil {
		return nil
	}

	if delta.ReadDeadline != nil {
		this.ReadDeadline = delta.ReadDeadline
	}

	if delta.WriteDeadline != nil {
		this.WriteDeadline = delta.WriteDeadline
	}

	if delta.HeartInterval != nil {
		this.HeartInterval = delta.HeartInterval
	}

	if delta.SendChanSize != nil {
		this.SendChanSize = delta.SendChanSize
	}

	if delta.MaxFrameSize != nil {
		this.MaxFrameSize = delta.MaxFrameSize
	}

	return this
}

func (this *Option) Merge(opts ...*Option) *Option {
	for _, opt := range opts {
		this.merge(opt)
	}
	return this
}
