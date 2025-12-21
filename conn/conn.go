package conn

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	flag_msg byte = 1 << iota
	flag_ping
	flag_pong
)

type Handler interface {
	HandleMsg(data []byte) error
}

type Conn struct {
	net.Conn
	// r        io.Reader
	r        *bufio.Reader
	w        *bufio.Writer
	sendChan chan *msg
	handler  Handler
	opt      *Option

	closed atomic.Bool // 原子状态标记
	ctx    context.Context
	cancel context.CancelFunc

	l        sync.Mutex // protect closeErr
	closeErr error
}

func New(ctx context.Context, conn net.Conn, handler Handler, opts ...*Option) (*Conn, error) {
	opt := Options().
		SetReadTimeout(5 * time.Second).
		SetWriteTimeout(5 * time.Second).
		SetReadTimeoutFactor(2.2).
		SetHeartInterval(5 * time.Second).
		SetSendChanTimeout(5 * time.Second).
		SetSendChanSize(1024).
		SetReadBufferLimitSize(100 * 1024 * 1024). //100M
		Merge(opts...)

	if opt.ReadTimeout == nil || *opt.ReadTimeout == 0 {
		timeout := time.Duration(float64(*opt.HeartInterval) * *opt.ReadTimeoutFactor)
		opt.ReadTimeout = &timeout
	}

	if opt.GenBufFn == nil {
		return nil, errors.New("GenBufFn is nil")
	}

	ctx, cancel := context.WithCancel(ctx)
	c := &Conn{
		Conn:     conn,
		r:        bufio.NewReader(conn),
		w:        bufio.NewWriter(conn),
		handler:  handler,
		opt:      &opt,
		ctx:      ctx,
		cancel:   cancel,
		sendChan: make(chan *msg, *opt.SendChanSize),
	}
	c.closed.Store(false)
	return c, nil
}

// WARNING: 非线程安全，由 writePump 独占调用
func (this *Conn) write(flag byte, data []byte, opts ...*Option) (err error) {
	return this.writes(flag, [][]byte{data}, opts...)
}

func (this *Conn) writes(flag byte, datas [][]byte, opts ...*Option) (err error) {
	opt := this.opt.Merge(opts...)
	length := 0
	for _, data := range datas {
		length += len(data)
	}
	var size [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(size[:], uint64(length)+1)

	var deadline time.Time
	if t := opt.WriteTimeout; t != nil {
		if *t != 0 {
			deadline = time.Now().Add(*t)
		}
	}

	if err = this.Conn.SetWriteDeadline(deadline); err != nil {
		return
	}

	if _, err = this.w.Write(size[:n]); err != nil {
		return
	}

	if err = this.w.WriteByte(flag); err != nil {
		return
	}

	for _, data := range datas {
		if _, err = this.w.Write(data); err != nil {
			return
		}
	}
	return
}

// WARNING: 非线程安全，由 readPump 独占调用
func (this *Conn) read(opts ...*Option) (flag byte, data []byte, err error) {
	opt := this.opt.Merge(opts...)
	if opt.GenBufFn == nil {
		err = fmt.Errorf("GenBufFn is nil")
		return
	}
	read_buf_limit_size := *opt.ReadBufferLimitSize
	var deadline time.Time
	if t := opt.ReadTimeout; t != nil {
		if *t > 0 {
			deadline = time.Now().Add(*t)
		}
	}
	if err = this.Conn.SetReadDeadline(deadline); err != nil {
		return
	}

	size, err := binary.ReadUvarint(this.r)
	if err != nil {
		return
	}

	if size > read_buf_limit_size {
		err = fmt.Errorf("frame size %d exceeds maximum %d", size, read_buf_limit_size)
		return
	}

	if size == 0 {
		err = fmt.Errorf("invalid frame size 0")
		return
	}

	flag, err = this.r.ReadByte()
	if err != nil {
		return
	}
	if flag == flag_ping || flag == flag_pong {
		return
	}

	size -= 1 // 减去 flag 的 1 字节

	data = opt.GenBufFn()
	if uint64(cap(data)) < size {
		data = make([]byte, size)
	}
	data = data[:size]
	_, err = io.ReadFull(this.r, data)
	// slog.Info("read", "data", data, "size", size, "err", err, "n", n)
	return
}

func (this *Conn) ping() (err error) {

	if err = this.write(flag_ping, []byte{}); err != nil {
		err = fmt.Errorf("ping:%w", err)
		return
	}

	if err = this.Flush(); err != nil {
		return err
	}

	return
}

func (this *Conn) pong() error {
	msg := msgPool.Get().(*msg)
	msg.flag = flag_pong
	select {
	case this.sendChan <- msg:
	default:
		msg.Release()
		// 如果发送缓冲区满，丢弃 PONG 是安全的，对方会在下一个周期重试 PING
		// 或者对方发送业务数据时也会刷新活跃状态
		slog.Warn("pong", "err", "send pong buffer full") //pong 失败不应该关闭链接
		return nil
	}
	return nil
}

// writePump 负责将 sendChan 中的数据写入连接，并维护心跳
// 采用“智能心跳”策略：仅在连接空闲时发送 PING
func (this *Conn) writePump() (err error) {
	heartInterval := *this.opt.HeartInterval
	// 发送检测周期设为心跳间隔的一半，确保有足够的冗余
	// keepAliveDuration := heartInterval / 2

	// 使用 Timer 实现弹性心跳
	timer := time.NewTimer(heartInterval)
	defer timer.Stop()

	for {
		select {
		case <-this.ctx.Done():
			return nil
		case msg, ok := <-this.sendChan:
			if !ok {
				return fmt.Errorf("sendChan closed")
			}

			// 发送数据（业务消息或 PONG）
			if err = this.writes(msg.flag, msg.data, msg.opt); err != nil {
				msg.Release()
				return err
			}
			msg.Release()
			// 2. 【关键策略】：检查通道里是否还有排队的数据？
			// 如果还有，就继续循环去拿，暂不 Flush，为了拼成大包。
			// 如果没有了，说明这波突发流量结束了，立刻 Flush 保证低延迟。
			if len(this.sendChan) > 0 {
				continue
			}

			if err = this.w.Flush(); err != nil {
				return
			}

			// 发送成功，连接处于活跃状态，重置 PING 定时器
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(heartInterval)

		case <-timer.C:
			// 定时器触发，说明 keepAliveDuration 时间内未发送任何数据
			// 发送 PING 维持连接活跃
			if err = this.ping(); err != nil {
				return err
			}
			// 发送 PING 后重置定时器
			timer.Reset(heartInterval)
		}
	}
}

// readPump 负责从连接读取数据，并作为“看门狗”检测连接超时
func (this *Conn) readPump() error {
	heartInterval := *this.opt.HeartInterval
	// 【修改点】优化超时策略
	// 发送间隔是  heartInterval。
	// 将超时设为 2.2 * heartInterval (或者 heartInterval + 2*time.Second)。
	// 意义：允许丢失 1 个心跳包 (1)，并允许第 2 个心跳包 (2) 晚到 20% 的时间。
	// 这比 2.0 倍敏感得多，能更快发现断连，同时防止轻微抖动导致的误断。
	readTimeout := time.Duration(float64(heartInterval) * *this.opt.ReadTimeoutFactor)
	for {
		// 每次读取前设置 DeadLine，给连接“续命”
		flag, body, err := this.read(Options().SetReadTimeout(readTimeout))
		if err != nil {
			// 如果超时，这里会返回 i/o timeout 错误
			return fmt.Errorf("read error: %w", err)
		}

		// 收到任何数据，说明连接是健康的。下一次循环会重新设置 ReadDeadline。

		switch flag {
		case flag_msg:
			if this.handler != nil {
				func() {
					defer func() {
						if r := recover(); r != nil {
							slog.Error("handler panic", "err", r)
						}
					}()
					if err := this.handler.HandleMsg(body); err != nil {
						slog.Error("handler msg", "err", err)
					}
				}()
			}
		case flag_ping:
			// 收到 PING，回复 PONG
			this.pong()
		case flag_pong:
			// 收到 PONG，仅表示对方活着，ReadDeadline 已自动刷新，无需操作
			// log.Println("receive pong")
		default:
			return fmt.Errorf("unknown flag: %d", flag)
		}
	}
}
