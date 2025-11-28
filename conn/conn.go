package conn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
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

type msg struct {
	flag byte
	data []byte
	opt  *Option
}

type Conn struct {
	net.Conn
	r        io.Reader
	w        *bufio.Writer
	sendChan chan *msg
	handler  Handler
	opt      *Option

	closed atomic.Bool // 原子状态标记
	done   chan struct{}

	l        sync.Mutex // protect closeErr
	closeErr error
}

func New(conn net.Conn, handler Handler, opts ...*Option) *Conn {
	opt := Options().
		SetDeadline(10 * time.Second).
		SetReadTimeoutFactor(2.2).
		SetHeartInterval(10 * time.Second).
		SetSendChanSize(100).
		SetMaxFrameSize(64 * 1024).
		Merge(opts...)
	c := &Conn{
		Conn:     conn,
		r:        bufio.NewReader(conn),
		w:        bufio.NewWriter(conn),
		handler:  handler,
		opt:      opt,
		done:     make(chan struct{}),
		sendChan: make(chan *msg, *opt.SendChanSize),
	}
	c.closed.Store(false)
	return c
}

// WARNING: 非线程安全，由 writePump 独占调用
func (this *Conn) write(flag byte, data []byte, opts ...*Option) (err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	length := len(data)
	var size [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(size[:], uint64(length)+1)

	if err = this.Conn.SetWriteDeadline(time.Now().Add(*opt.WriteDeadline)); err != nil {
		return
	}

	if _, err = this.w.Write(size[:n]); err != nil {
		return
	}

	if err = this.w.WriteByte(flag); err != nil {
		return
	}

	if _, err = this.w.Write(data); err != nil {
		return
	}
	return this.w.Flush()
}

// WARNING: 非线程安全，由 readPump 独占调用
func (this *Conn) read(opts ...*Option) (flag byte, data []byte, err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	max_frame_size := *opt.MaxFrameSize

	if err = this.Conn.SetReadDeadline(time.Now().Add(*opt.ReadDeadline)); err != nil {
		return
	}

	size, err := binary.ReadUvarint(this.r.(io.ByteReader))
	if err != nil {
		return
	}
	if size > max_frame_size {
		err = fmt.Errorf("frame size %d exceeds maximum %d", size, max_frame_size)
		return
	}
	if size == 0 {
		err = fmt.Errorf("invalid frame size 0")
		return
	}

	raw_data := make([]byte, size)
	if _, err = io.ReadFull(this.r, raw_data); err != nil {
		return
	}
	flag = raw_data[0]
	data = raw_data[1:]
	return
}

func (this *Conn) ping() error {
	if err := this.write(flag_ping, []byte{}); err != nil {
		return fmt.Errorf("ping:%w", err)
	}
	return nil
}

func (this *Conn) pong() error {
	select {
	case this.sendChan <- &msg{
		flag: flag_pong,
		data: nil,
	}:
	default:
		// 如果发送缓冲区满，丢弃 PONG 是安全的，对方会在下一个周期重试 PING
		// 或者对方发送业务数据时也会刷新活跃状态
		return fmt.Errorf("send pong buffer full")
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
		case <-this.done:
			return nil

		case msg, ok := <-this.sendChan:
			if !ok {
				return fmt.Errorf("sendChan closed")
			}

			// 发送数据（业务消息或 PONG）
			if err = this.write(msg.flag, msg.data, msg.opt); err != nil {
				return err
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
		flag, body, err := this.read(Options().SetReadDeadline(readTimeout))
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
							log.Printf("handler panic: %v", r)
						}
					}()
					if err := this.handler.HandleMsg(body); err != nil {
						log.Printf("handle msg error: %v", err)
					}
				}()
			}
		case flag_ping:
			// 收到 PING，回复 PONG
			if err := this.pong(); err != nil {
				return err
			}
		case flag_pong:
			// 收到 PONG，仅表示对方活着，ReadDeadline 已自动刷新，无需操作
			// log.Println("receive pong")
		default:
			return fmt.Errorf("unknown flag: %d", flag)
		}
	}
}
