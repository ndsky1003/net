package conn

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
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
	r              io.Reader
	w              *bufio.Writer
	sendChan       chan *msg
	last_msg_nano  int64 //最新业务消息时间
	last_pong_nano int64 //最新心跳消息时间
	handler        Handler
	opt            *Option

	closed atomic.Bool // 原子状态标记
	done   chan struct{}
}

func New(conn net.Conn, handler Handler, opts ...*Option) *Conn {
	opt := Options().
		SetDeadline(10 * time.Second).
		SetHeartInterval(30 * time.Second).
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
	now := time.Now().UnixNano()
	atomic.StoreInt64(&c.last_msg_nano, now)
	atomic.StoreInt64(&c.last_pong_nano, now)
	return c
}

// WARNING: 这个函数是非线程安全的，需要外部调用者保证
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

// WARNING: 非线程安全
func (this *Conn) read(opts ...*Option) (flag byte, data []byte, err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	max_frame_size := *opt.MaxFrameSize
	this.Conn.SetReadDeadline(time.Now().Add(*opt.ReadDeadline))
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

// NOTE: 这里只能往队列里面放，如果直接写会有多线程问题，read和write是2个不同的goroutine，会引发var ErrShortWrite = errors.New("short write")
func (this *Conn) pong() error {
	// if err := this.write(flag_pong, []byte{}); err != nil {
	// 	return fmt.Errorf("pong:%w", err)
	// }
	select {
	case this.sendChan <- &msg{ //如果 Close() 先执行：sendChan 被关闭，select 会检测到通道关闭并panic
		flag: flag_pong,
		data: nil,
	}:
	default:
		return fmt.Errorf("send pong buffer full")
	}
	return nil
}

func (this *Conn) writePump() (err error) {
	heart_interval := *this.opt.HeartInterval
	ticker := time.NewTicker(heart_interval / 2) //探测周期缩短一半
	defer ticker.Stop()
	for {
		select {
		case <-this.done:
			return
		case msg, ok := <-this.sendChan:
			if !ok {
				err = fmt.Errorf("sendChan has exhaust")
				return
			}
			if this.closed.Load() { // 额外检查
				return fmt.Errorf("connection closed ")
			}
			if err = this.write(msg.flag, msg.data, msg.opt); err != nil {
				return
			}
		case <-ticker.C:
			if this.closed.Load() { // 额外检查
				return fmt.Errorf("connection closed ")
			}
			usage := float64(len(this.sendChan)) / float64(cap(this.sendChan))
			if usage > 0.8 {
				log.Printf("send buffer usage: %.1f%%, consider increasing size", usage*100)
			}
			now := time.Now().UnixNano()
			heartIntervalNano := int64(heart_interval)
			// 1. 超时检查：检查距离上次 PONG 有多久
			lastPong := atomic.LoadInt64(&this.last_pong_nano)
			// 增加一个500ms的宽限期
			if now-lastPong > heartIntervalNano+int64(500*time.Millisecond) {
				return fmt.Errorf("pong timeout, last pong: %v ago", time.Duration(now-lastPong))
			}

			// 2. 发送 PING 检查：检查距离上次业务消息有多久
			lastMsg := atomic.LoadInt64(&this.last_msg_nano)
			if now-lastMsg > heartIntervalNano {
				if err = this.ping(); err != nil {
					return
				}
			}
		}
	}
}

func (this *Conn) readPump() error {
	for {
		select {
		case <-this.done:
			return fmt.Errorf("connection closed")
		default:
			flag, body, err := this.read()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				return fmt.Errorf("1:%w", err)
			}
			switch flag {
			case flag_msg:
				now := time.Now().UnixNano()
				atomic.StoreInt64(&this.last_msg_nano, now)
				atomic.StoreInt64(&this.last_pong_nano, now) // 关键修正：业务消息也代表连接存活
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
				if this.closed.Load() {
					return fmt.Errorf("connection closed")
				}
				log.Println("receive ping msg")
				if err := this.pong(); err != nil {
					return err
				}
			case flag_pong:
				atomic.StoreInt64(&this.last_pong_nano, time.Now().UnixNano())
				log.Println("receive pong msg")
				// 正常处理，时间已更新
			default:
				return fmt.Errorf("unknown flag: %d", flag)
			}
		}
	}
}
