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
	r         io.Reader
	w         *bufio.Writer
	sendChan  chan *msg
	pong_time int64
	handler   Handler
	opt       *Option

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
	return c
}

// WARNING: 这个函数是非线程安全的，需要外部调用者保证
func (this *Conn) write(flag byte, data []byte, opts ...*Option) (err error) {
	opt := Options().Merge(this.opt).Merge(opts...)
	length := len(data)
	var size [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(size[:], uint64(length)+1)
	if err = this.Conn.SetWriteDeadline(time.Now().Add(*opt.WriteDeadline)); err != nil {
		err = fmt.Errorf("err1:%w", err)
		return
	}
	if _, err = this.w.Write(size[:n]); err != nil {
		err = fmt.Errorf("err2:%w", err)
		return
	}

	if err = this.w.WriteByte(flag); err != nil {
		err = fmt.Errorf("err3:%w", err)
		return
	}

	if _, err = this.w.Write(data); err != nil {
		err = fmt.Errorf("err4:%w", err)
		return
	}
	if err = this.w.Flush(); err != nil {
		err = fmt.Errorf("err5:%w", err)
		return
	}
	return
}

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
func (this *Conn) pong() error {
	if err := this.write(flag_pong, []byte{}); err != nil {
		return fmt.Errorf("pong:%w", err)
	}
	return nil
}

func (this *Conn) writePump() (err error) {
	heart_interval := *this.opt.HeartInterval
	ticker := time.NewTicker(heart_interval)
	//模拟一个ping/pong响应的是100毫秒
	atomic.CompareAndSwapInt64(&this.pong_time, 0, time.Now().Add(100*time.Millisecond).UnixNano())
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
			lastPongNano := atomic.LoadInt64(&this.pong_time)
			currentNano := time.Now().UnixNano()
			delta := currentNano - lastPongNano
			if float64(delta)-float64(heart_interval) > float64(500*time.Millisecond) {
				return fmt.Errorf("pong timeout:%v,%v,%v", delta, heart_interval, delta-int64(heart_interval))
			}
			// log.Println("delta:", delta, "heart_interval3/4:", float64(heart_interval)*3/4)
			if float64(delta) > float64(heart_interval)*3/4 {
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
			atomic.StoreInt64(&this.pong_time, time.Now().UnixNano())
			switch flag {
			case flag_msg:
				if this.handler != nil {
					go func() {
						defer func() {
							if r := recover(); r != nil {
								log.Printf("handler panic: %v", r)
							}
						}()
						this.handler.HandleMsg(body)
					}()
				}
			case flag_ping:
				if this.closed.Load() {
					return fmt.Errorf("connection closed")
				}
				// log.Println("receive ping msg")
				if err := this.pong(); err != nil {
					return err
				}
			case flag_pong:
				// log.Println("receive pong msg")
				// 正常处理，时间已更新
			default:
				return fmt.Errorf("unknown flag: %d", flag)
			}
		}
	}
}
